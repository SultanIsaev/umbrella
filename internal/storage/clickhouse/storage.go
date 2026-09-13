package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/SultanIsaev/umbrella/internal/storage"
)

// Config задаёт подключение и параметры батчинга.
type Config struct {
	// DSN — строка подключения, например
	// "clickhouse://user:pass@host:9000/database".
	DSN string
	// Table — таблица-приёмник. Ожидаемая схема:
	//   timestamp DateTime, source String, fields String (JSON-encoded)
	// Создание таблицы/миграции — вне ответственности этого пакета.
	Table string
	// BatchSize/FlushInterval — см. Batcher.
	BatchSize     int
	FlushInterval time.Duration
}

// Storage реализует storage.Storage поверх ClickHouse: события копятся в
// Batcher (см. batcher.go — логика батчинга полностью отделена от самой
// отправки) и улетают в Table через batch INSERT, а не по одной строке.
type Storage struct {
	conn    driver.Conn
	table   string
	batcher *Batcher

	// runDone закрывается, когда Run фактически вернулся — Close ждёт
	// этого перед закрытием conn, чтобы не оборвать ещё летящий в Run
	// финальный flush тем же соединением.
	runDone chan struct{}
}

var _ storage.Storage = (*Storage)(nil)

// New открывает соединение с ClickHouse и проверяет его (Ping), но не
// запускает батчинг сам — как и ingest.Listener/pipeline.Pool, Run
// запускает вызывающий код (обычно через errgroup), чтобы владение
// горутиной было явным на стороне оркестратора.
func New(ctx context.Context, cfg Config) (*Storage, error) {
	if cfg.Table == "" {
		return nil, fmt.Errorf("clickhouse: table must not be empty")
	}

	opts, err := clickhouse.ParseDSN(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: parse dsn: %w", err)
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open: %w", err)
	}
	if err = conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse: ping: %w", err)
	}

	s := &Storage{conn: conn, table: cfg.Table, runDone: make(chan struct{})}

	batcher, err := NewBatcher(cfg.BatchSize, cfg.FlushInterval, s.flushBatch)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	s.batcher = batcher

	return s, nil
}

// Run запускает цикл батчинга. Как и ingest.Listener.Run/pipeline.Pool.Run,
// вызывающий код сам оборачивает его в горутину (errgroup и т.п.); Storage
// сам отслеживает лишь факт возврата Run (см. runDone), чтобы Close мог
// безопасно дождаться его перед закрытием соединения.
func (s *Storage) Run(ctx context.Context) error {
	defer close(s.runDone)
	return s.batcher.Run(ctx)
}

// Write реализует storage.Storage — ставит события в очередь батчера;
// фактическая отправка в ClickHouse происходит асинхронно в Run.
func (s *Storage) Write(ctx context.Context, events []storage.Event) error {
	return s.batcher.Write(ctx, events)
}

// Close сигнализирует Run завершиться (CloseInput — Run доработает и
// вернётся), дожидается его фактического возврата и только потом закрывает
// соединение с ClickHouse — иначе можно оборвать ещё летящий в Run
// последний flush тем же conn. Требует, чтобы Run уже был запущен
// вызывающим кодом (как и ingest.Listener/pipeline.Pool — Run стартует
// отдельно, оркестратор решает, когда всё остановить); вызов Close без
// когда-либо запущенного Run заблокируется навсегда.
func (s *Storage) Close() error {
	s.batcher.CloseInput()
	<-s.runDone
	return s.conn.Close()
}

// flushBatch — единственное место, где Batcher соприкасается с реальным
// ClickHouse: batch INSERT вместо построчного, как и требует doc.go пакета.
func (s *Storage) flushBatch(ctx context.Context, events []storage.Event) error {
	batch, err := s.conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s (timestamp, source, fields)", s.table))
	if err != nil {
		return fmt.Errorf("clickhouse: prepare batch: %w", err)
	}
	defer batch.Close()

	for _, e := range events {
		fields, err := json.Marshal(e.Fields)
		if err != nil {
			return fmt.Errorf("clickhouse: marshal fields: %w", err)
		}
		if err := batch.Append(time.Unix(e.Timestamp, 0), e.Source, string(fields)); err != nil {
			return fmt.Errorf("clickhouse: append row: %w", err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("clickhouse: send batch: %w", err)
	}
	return nil
}
