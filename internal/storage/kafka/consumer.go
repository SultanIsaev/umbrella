package kafka

import (
	"context"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"
)

// Handler обрабатывает одно потреблённое сообщение. Ненулевая ошибка
// означает, что сообщение не обработано и не должно коммититься — при
// at-least-once доставке это приводит к повторной доставке (возможное
// переобработка), что и есть осознанный компромисс здесь вместо тихой
// потери. Consumer.Run считает ошибку Handler фатальной и останавливает
// весь цикл — retry с backoff или dead-letter queue после N попыток это
// уже отдельная, не покрытая этим шагом задача.
type Handler func(ctx context.Context, msg kafkago.Message) error

// messageFetcher — тот срез методов *kafka.Reader, который нужен
// Consumer.Run. *kafka.Reader реализует его естественно, без обёртки —
// это позволяет тестировать consume-process-commit логику без реального
// брокера, тот же принцип разделения, что и Batcher/FlushFunc в
// internal/storage/clickhouse.
type messageFetcher interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// ConsumerConfig задаёт consumer group.
type ConsumerConfig struct {
	Brokers []string
	// GroupID — реальный consumer group: kafka-go сам берёт на себя
	// distribution партиций между всеми читателями с одним GroupID и
	// ребаланс при их появлении/исчезновении.
	GroupID string
	Topic   string
}

// Consumer читает из consumer group с ручным коммитом: at-least-once
// доставка (коммит только после успешного Handler), graceful shutdown
// через context.
type Consumer struct {
	reader messageFetcher
	closer func() error
	handle Handler
}

// NewConsumer создаёт читателя consumer group для cfg.Topic.
//
// Проверяет то же самое, что kafkago.ReaderConfig.Validate (пустые Brokers/
// Topic) сам, до вызова kafkago.NewReader — та при невалидном конфиге не
// возвращает ошибку, а паникует, что для конструктора неприемлемо: ошибка
// вызывающего (забыл проставить поле) не должна ронять процесс паникой.
func NewConsumer(cfg ConsumerConfig, handle Handler) (*Consumer, error) {
	if handle == nil {
		return nil, fmt.Errorf("kafka: handle must not be nil")
	}
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka: brokers must not be empty")
	}
	if cfg.GroupID == "" {
		return nil, fmt.Errorf("kafka: GroupID must not be empty")
	}
	if cfg.Topic == "" {
		return nil, fmt.Errorf("kafka: topic must not be empty")
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: cfg.Brokers,
		GroupID: cfg.GroupID,
		Topic:   cfg.Topic,
	})
	return &Consumer{reader: reader, closer: reader.Close, handle: handle}, nil
}

// Run — цикл consume-process-commit. Backpressure здесь неявный: следующее
// сообщение забирается только после того, как Handler и коммит текущего
// оба завершились, поэтому медленный Handler сам собой ограничивает темп
// потребления — отдельного механизма не требуется.
//
// Отмена ctx — ожидаемый сигнал остановки (тот же принцип, что и
// ingest.Listener.Run/clickhouse.Batcher.Run): Run возвращается без
// ошибки, а не с ctx.Err(). Close вызывать после того, как Run уже
// вернулся, а не как механизм остановки.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("kafka: fetch message: %w", err)
		}

		if err := c.handle(ctx, msg); err != nil {
			return fmt.Errorf("kafka: handle message (topic=%s partition=%d offset=%d): %w",
				msg.Topic, msg.Partition, msg.Offset, err)
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("kafka: commit message: %w", err)
		}
	}
}

// Close закрывает читателя. Вызывать после того, как Run уже вернулся —
// как и ingest.Listener/clickhouse.Storage, оркестратор дожидается
// остановки, затем закрывает ресурс.
func (c *Consumer) Close() error {
	return c.closer()
}
