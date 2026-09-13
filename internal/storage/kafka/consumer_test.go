package kafka

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// fakeFetcher реализует messageFetcher без реального брокера: отдаёт
// messages по одному, затем либо возвращает fetchErr, либо (если он не
// задан) блокируется до отмены ctx — как и настоящий *kafka.Reader.FetchMessage
// в отсутствие новых сообщений.
type fakeFetcher struct {
	mu        sync.Mutex
	messages  []kafkago.Message
	idx       int
	committed []kafkago.Message
	fetchErr  error
}

func (f *fakeFetcher) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	f.mu.Lock()
	if f.idx < len(f.messages) {
		m := f.messages[f.idx]
		f.idx++
		f.mu.Unlock()
		return m, nil
	}
	err := f.fetchErr
	f.mu.Unlock()

	if err != nil {
		return kafkago.Message{}, err
	}
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (f *fakeFetcher) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.committed = append(f.committed, msgs...)
	return nil
}

func (f *fakeFetcher) snapshotCommitted() []kafkago.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]kafkago.Message, len(f.committed))
	copy(out, f.committed)
	return out
}

func msgAt(offset int64) kafkago.Message {
	return kafkago.Message{Topic: "t", Partition: 0, Offset: offset, Value: []byte(fmt.Sprintf("v%d", offset))}
}

func TestConsumer_AtLeastOnce_CommitsAfterSuccess(t *testing.T) {
	fetcher := &fakeFetcher{messages: []kafkago.Message{msgAt(0), msgAt(1), msgAt(2)}}

	var handled []kafkago.Message
	var mu sync.Mutex
	handle := func(_ context.Context, msg kafkago.Message) error {
		mu.Lock()
		handled = append(handled, msg)
		mu.Unlock()
		return nil
	}

	c := &Consumer{reader: fetcher, closer: func() error { return nil }, handle: handle}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- c.Run(ctx) }()

	require.Eventually(t, func() bool {
		return len(fetcher.snapshotCommitted()) == 3
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}

	require.Len(t, handled, 3)
	require.Equal(t, []kafkago.Message{msgAt(0), msgAt(1), msgAt(2)}, fetcher.snapshotCommitted())
}

func TestConsumer_HandlerError_StopsWithoutCommittingFailedMessage(t *testing.T) {
	fetcher := &fakeFetcher{messages: []kafkago.Message{msgAt(0), msgAt(1), msgAt(2)}}
	wantErr := fmt.Errorf("boom")

	handle := func(_ context.Context, msg kafkago.Message) error {
		if msg.Offset == 1 {
			return wantErr
		}
		return nil
	}

	c := &Consumer{reader: fetcher, closer: func() error { return nil }, handle: handle}

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- c.Run(context.Background()) }()

	select {
	case err := <-runErrCh:
		require.ErrorIs(t, err, wantErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after handler error")
	}

	// Смещение 0 успешно обработано и закоммичено; смещение 1 упало и не
	// закоммичено (at-least-once: будет передоставлено при перезапуске);
	// до смещения 2 дело не дошло вовсе.
	require.Equal(t, []kafkago.Message{msgAt(0)}, fetcher.snapshotCommitted())
}

func TestConsumer_ContextCancellation_GracefulStop(t *testing.T) {
	fetcher := &fakeFetcher{} // сообщений нет — FetchMessage блокируется до ctx
	handle := func(context.Context, kafkago.Message) error { return nil }

	c := &Consumer{reader: fetcher, closer: func() error { return nil }, handle: handle}

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- c.Run(ctx) }()

	cancel()

	select {
	case err := <-runErrCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}

func TestConsumer_FetchError_Propagates(t *testing.T) {
	wantErr := fmt.Errorf("broker unreachable")
	fetcher := &fakeFetcher{fetchErr: wantErr}
	handle := func(context.Context, kafkago.Message) error { return nil }

	c := &Consumer{reader: fetcher, closer: func() error { return nil }, handle: handle}

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- c.Run(context.Background()) }()

	select {
	case err := <-runErrCh:
		require.ErrorIs(t, err, wantErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after fetch error")
	}
}

func TestNewConsumer_Validation(t *testing.T) {
	noopHandle := func(context.Context, kafkago.Message) error { return nil }

	tests := []struct {
		name    string
		cfg     ConsumerConfig
		handle  Handler
		wantErr bool
	}{
		{name: "handle == nil", cfg: ConsumerConfig{Brokers: []string{"localhost:9092"}, GroupID: "g", Topic: "t"}, handle: nil, wantErr: true},
		{name: "Brokers пуст", cfg: ConsumerConfig{GroupID: "g", Topic: "t"}, handle: noopHandle, wantErr: true},
		{name: "GroupID пуст", cfg: ConsumerConfig{Brokers: []string{"localhost:9092"}, Topic: "t"}, handle: noopHandle, wantErr: true},
		{name: "Topic пуст", cfg: ConsumerConfig{Brokers: []string{"localhost:9092"}, GroupID: "g"}, handle: noopHandle, wantErr: true},
		{name: "валидная конфигурация", cfg: ConsumerConfig{Brokers: []string{"localhost:9092"}, GroupID: "g", Topic: "t"}, handle: noopHandle, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewConsumer(tt.cfg, tt.handle)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, c)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, c)
			require.NoError(t, c.Close())
		})
	}
}
