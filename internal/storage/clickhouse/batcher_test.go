package clickhouse

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/SultanIsaev/umbrella/internal/storage"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// recordingFlush — фейковый FlushFunc: каждый вызов кладёт КОПИЮ батча в
// канал (см. предупреждение в doc-комментарии FlushFunc про переиспользуемую
// память) — тесты читают из этого канала вместо time.Sleep.
func recordingFlush(calls chan<- []storage.Event) FlushFunc {
	return func(_ context.Context, events []storage.Event) error {
		cp := make([]storage.Event, len(events))
		copy(cp, events)
		calls <- cp
		return nil
	}
}

func event(source string) storage.Event {
	return storage.Event{Source: source}
}

func recvBatch(t *testing.T, calls <-chan []storage.Event) []storage.Event {
	t.Helper()
	select {
	case b := <-calls:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("flush was not called in time")
		return nil
	}
}

func requireNoBatch(t *testing.T, calls <-chan []storage.Event) {
	t.Helper()
	select {
	case b := <-calls:
		t.Fatalf("unexpected flush: %v", b)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNewBatcher_Validation(t *testing.T) {
	noopFlush := func(context.Context, []storage.Event) error { return nil }

	tests := []struct {
		name          string
		batchSize     int
		flushInterval time.Duration
		flush         FlushFunc
		wantErr       bool
	}{
		{name: "batchSize <= 0", batchSize: 0, flushInterval: time.Second, flush: noopFlush, wantErr: true},
		{name: "flushInterval <= 0", batchSize: 10, flushInterval: 0, flush: noopFlush, wantErr: true},
		{name: "flush == nil", batchSize: 10, flushInterval: time.Second, flush: nil, wantErr: true},
		{name: "валидные параметры", batchSize: 10, flushInterval: time.Second, flush: noopFlush, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewBatcher(tt.batchSize, tt.flushInterval, tt.flush)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, b)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, b)
		})
	}
}

// TestBatcher_FlushOnSize — batchSize достигнут раньше, чем flushInterval:
// таймер настолько большой, что не сработает за время теста.
func TestBatcher_FlushOnSize(t *testing.T) {
	calls := make(chan []storage.Event, 10)
	b, err := NewBatcher(3, time.Hour, recordingFlush(calls))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		_ = b.Run(ctx)
		close(runDone)
	}()

	require.NoError(t, b.Write(ctx, []storage.Event{event("a"), event("b"), event("c")}))

	got := recvBatch(t, calls)
	require.Equal(t, []storage.Event{event("a"), event("b"), event("c")}, got)

	cancel()
	<-runDone
}

// TestBatcher_FlushOnTimeout — batchSize недостижим за время теста, флаш
// происходит только по таймеру.
func TestBatcher_FlushOnTimeout(t *testing.T) {
	calls := make(chan []storage.Event, 10)
	b, err := NewBatcher(100, 50*time.Millisecond, recordingFlush(calls))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		_ = b.Run(ctx)
		close(runDone)
	}()

	require.NoError(t, b.Write(ctx, []storage.Event{event("a")}))

	got := recvBatch(t, calls)
	require.Equal(t, []storage.Event{event("a")}, got)

	cancel()
	<-runDone
}

// TestBatcher_EmptyBatchNotFlushed — таймер срабатывает, но копить было
// нечего: flush не должен вызываться на пустом батче.
func TestBatcher_EmptyBatchNotFlushed(t *testing.T) {
	calls := make(chan []storage.Event, 10)
	b, err := NewBatcher(100, 30*time.Millisecond, recordingFlush(calls))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		_ = b.Run(ctx)
		close(runDone)
	}()

	requireNoBatch(t, calls)

	cancel()
	<-runDone
}

// TestBatcher_GracefulStop — CloseInput должен привести к досылке
// финального неполного батча и штатному завершению Run (err == nil).
func TestBatcher_GracefulStop(t *testing.T) {
	calls := make(chan []storage.Event, 10)
	b, err := NewBatcher(100, time.Hour, recordingFlush(calls))
	require.NoError(t, err)

	ctx := context.Background()
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- b.Run(ctx)
	}()

	require.NoError(t, b.Write(ctx, []storage.Event{event("a"), event("b")}))
	b.CloseInput()

	got := recvBatch(t, calls)
	require.Equal(t, []storage.Event{event("a"), event("b")}, got)

	select {
	case err := <-runErrCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after CloseInput")
	}
}

func TestBatcher_ContextCancellationStopsRun(t *testing.T) {
	calls := make(chan []storage.Event, 10)
	b, err := NewBatcher(100, time.Hour, recordingFlush(calls))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- b.Run(ctx)
	}()

	cancel()

	select {
	case err := <-runErrCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}

func TestBatcher_FlushErrorPropagates(t *testing.T) {
	wantErr := fmt.Errorf("boom")
	b, err := NewBatcher(1, time.Hour, func(context.Context, []storage.Event) error {
		return wantErr
	})
	require.NoError(t, err)

	ctx := context.Background()
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- b.Run(ctx)
	}()

	require.NoError(t, b.Write(ctx, []storage.Event{event("a")}))

	select {
	case err := <-runErrCh:
		require.ErrorIs(t, err, wantErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after flush error")
	}
}

func TestBatcher_ConcurrentWrite(t *testing.T) {
	calls := make(chan []storage.Event, 1000)
	b, err := NewBatcher(5, 20*time.Millisecond, recordingFlush(calls))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		_ = b.Run(ctx)
		close(runDone)
	}()

	const goroutines = 8
	const perGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				require.NoError(t, b.Write(ctx, []storage.Event{event("x")}))
			}
		}()
	}
	wg.Wait()

	total := 0
	deadline := time.After(3 * time.Second)
	for total < goroutines*perGoroutine {
		select {
		case batch := <-calls:
			total += len(batch)
		case <-deadline:
			t.Fatalf("only %d/%d events flushed in time", total, goroutines*perGoroutine)
		}
	}
	require.Equal(t, goroutines*perGoroutine, total)

	cancel()
	<-runDone
}
