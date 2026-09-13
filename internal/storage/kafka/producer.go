package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/SultanIsaev/umbrella/internal/storage"
)

// messageWriter — тот срез методов *kafka.Writer, который нужен Producer —
// тот же принцип тестируемости без брокера, что и messageFetcher/Consumer.
type messageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// defaultBatchTimeout — во сколько раз меньше дефолта kafka-go (1с). Write
// зовёт WriteMessages синхронно с одним пакетом записей за раз (обычно
// заметно меньше дефолтного BatchSize=100) — при дефолтном BatchTimeout=1с
// каждый такой вызов блокируется почти на секунду в ожидании таймаута, а не
// заполнения батча. 10 мс — достаточно мало, чтобы не создавать эту задержку, но
// оставляет возможность настоящего батчинга при более высоком трафике.
const defaultBatchTimeout = 10 * time.Millisecond

// ProducerConfig задаёт топик-приёмник. В отличие от
// internal/storage/clickhouse, отдельный Batcher здесь не нужен:
// kafka.Writer уже батчит сам (BatchSize/BatchTimeout) — это забота
// kafka-go, не наша; но дефолт BatchTimeout самого kafka-go (1с) слишком
// велик для нашего паттерна вызова — см. defaultBatchTimeout.
type ProducerConfig struct {
	Brokers []string
	Topic   string
	// BatchTimeout — 0 означает defaultBatchTimeout, не дефолт kafka-go.
	BatchTimeout time.Duration
}

// Producer реализует storage.Storage поверх топика Kafka — роль "durable
// очереди между парсингом и internal/storage/clickhouse" из doc.go пакета.
type Producer struct {
	writer messageWriter
}

var _ storage.Storage = (*Producer)(nil)

func NewProducer(cfg ProducerConfig) (*Producer, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka: brokers must not be empty")
	}
	if cfg.Topic == "" {
		return nil, fmt.Errorf("kafka: topic must not be empty")
	}

	batchTimeout := cfg.BatchTimeout
	if batchTimeout <= 0 {
		batchTimeout = defaultBatchTimeout
	}

	writer := &kafkago.Writer{
		Addr:         kafkago.TCP(cfg.Brokers...),
		Topic:        cfg.Topic,
		BatchTimeout: batchTimeout,
	}
	return &Producer{writer: writer}, nil
}

// Write сериализует каждое событие в JSON и публикует одним вызовом
// WriteMessages — партиционирование и батчинг на стороне kafka-go.
func (p *Producer) Write(ctx context.Context, events []storage.Event) error {
	if len(events) == 0 {
		return nil
	}

	msgs := make([]kafkago.Message, len(events))
	for i, e := range events {
		value, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("kafka: marshal event: %w", err)
		}
		msgs[i] = kafkago.Message{Value: value}
	}

	if err := p.writer.WriteMessages(ctx, msgs...); err != nil {
		return fmt.Errorf("kafka: write messages: %w", err)
	}
	return nil
}

// Close закрывает writer.
func (p *Producer) Close() error {
	return p.writer.Close()
}
