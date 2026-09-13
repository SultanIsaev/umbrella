package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/SultanIsaev/umbrella/internal/storage"
)

// FlushFunc отправляет один накопленный батч куда-либо. Отделена от Batcher
// намеренно: логика size-or-timeout ничего не знает про ClickHouse и
// тестируется без него (см. batcher_test.go) — только через Batcher.Run
// эта логика в проде сводится с реальной отправкой (см. storage.go).
//
// flush не должен сохранять events после возврата: тот же участок памяти
// переиспользуется под следующий батч (Batcher.Run не копирует его заново
// после сброса длины).
type FlushFunc func(ctx context.Context, events []storage.Event) error

// Batcher копит события, пока не наберётся batchSize штук либо не истечёт
// flushInterval — что раньше, через select+time.Timer, без time.Sleep.
type Batcher struct {
	in            chan storage.Event
	flush         FlushFunc
	batchSize     int
	flushInterval time.Duration
}

// NewBatcher создаёт батчер. Буфер in равен batchSize: этого достаточно,
// чтобы всплеск ровно в один батч не сериализовался на приёме поштучно, а
// дальнейший backpressure (Write блокируется) — осознанное решение, тот же
// принцип, что и блокирующая запись между стадиями в internal/pipeline.
func NewBatcher(batchSize int, flushInterval time.Duration, flush FlushFunc) (*Batcher, error) {
	if batchSize <= 0 {
		return nil, fmt.Errorf("clickhouse: batchSize must be positive, got %d", batchSize)
	}
	if flushInterval <= 0 {
		return nil, fmt.Errorf("clickhouse: flushInterval must be positive, got %s", flushInterval)
	}
	if flush == nil {
		return nil, fmt.Errorf("clickhouse: flush must not be nil")
	}
	return &Batcher{
		in:            make(chan storage.Event, batchSize),
		flush:         flush,
		batchSize:     batchSize,
		flushInterval: flushInterval,
	}, nil
}

// Write ставит события в очередь на батчинг. Блокирующая отправка —
// backpressure распространяется назад к вызывающему коду естественно,
// вместо молчаливой потери событий при переполнении.
func (b *Batcher) Write(ctx context.Context, events []storage.Event) error {
	for _, e := range events {
		select {
		case b.in <- e:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// CloseInput сигнализирует Run, что новых событий больше не будет — Run
// доработает то, что уже накоплено (включая финальный неполный батч), и
// вернётся. Закрывать имеет право только оркестратор, дождавшийся
// остановки всех писателей Write (тот же принцип, что и у out в
// internal/ingest/internal/pipeline).
func (b *Batcher) CloseInput() {
	close(b.in)
}

// Run — основной цикл size-or-timeout батчинга. Должен быть запущен в
// отдельной горутине вызывающим кодом (тот же паттерн, что и ingest.Listener.Run/pipeline.Pool.Run).
//
// Закрытие in (см. CloseInput) — штатная, "мягкая" остановка: Run досылает
// последний неполный батч, если он не пуст, и возвращается без ошибки.
//
// Отмена ctx — "жёсткая" остановка: Run возвращается немедленно и без
// ошибки (как и ingest.Listener.Run — это ожидаемый сигнал остановки,
// например SIGTERM через errgroup, а не сбой приложения), но то, что уже
// накоплено в текущем батче, — теряется. ctx также ограничивает каждый
// вызов flush — сетевой поход в ClickHouse не должен виснуть бесконечно.
func (b *Batcher) Run(ctx context.Context) error {
	timer := time.NewTimer(b.flushInterval)
	defer timer.Stop()

	batch := make([]storage.Event, 0, b.batchSize)

	flushNow := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := b.flush(ctx, batch)
		batch = batch[:0]
		return err
	}

	for {
		select {
		case e, ok := <-b.in:
			if !ok {
				return flushNow()
			}
			batch = append(batch, e)
			if len(batch) >= b.batchSize {
				if err := flushNow(); err != nil {
					return err
				}
				// Таймер мог успеть сработать независимо, пока мы ждали
				// это самое событие в select — сливаем его канал, если
				// значение уже там, прежде чем переиспользовать таймер
				// (стандартный безопасный паттерн Reset после Stop==false).
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(b.flushInterval)
			}

		case <-timer.C:
			if err := flushNow(); err != nil {
				return err
			}
			timer.Reset(b.flushInterval)

		case <-ctx.Done():
			// Отмена ctx — ожидаемый сигнал остановки (как и в
			// ingest.Listener.Run), а не ошибка приложения: без этого
			// graceful shutdown по SIGTERM выглядел бы как сбой (ненулевой
			// код выхода, ctx.Err() в stderr) даже при штатной остановке.
			return nil
		}
	}
}
