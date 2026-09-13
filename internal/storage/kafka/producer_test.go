package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"

	"github.com/SultanIsaev/umbrella/internal/storage"
)

// fakeWriter реализует messageWriter без реального брокера.
type fakeWriter struct {
	mu     sync.Mutex
	sent   []kafkago.Message
	err    error
	closed bool
}

func (f *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msgs...)
	return nil
}

func (f *fakeWriter) Close() error {
	f.closed = true
	return nil
}

func TestProducer_Write_MarshalsAndSends(t *testing.T) {
	w := &fakeWriter{}
	p := &Producer{writer: w}

	events := []storage.Event{
		{Timestamp: 100, Source: "a", Fields: storage.Fields{{Key: "x", Value: float64(1)}}},
		{Timestamp: 200, Source: "b", Fields: storage.Fields{{Key: "x", Value: float64(2)}}},
	}

	require.NoError(t, p.Write(context.Background(), events))
	require.Len(t, w.sent, 2)

	for i, msg := range w.sent {
		var got storage.Event
		require.NoError(t, json.Unmarshal(msg.Value, &got))
		require.Equal(t, events[i], got)
	}
}

func TestProducer_Write_EmptyBatch(t *testing.T) {
	w := &fakeWriter{}
	p := &Producer{writer: w}

	require.NoError(t, p.Write(context.Background(), nil))
	require.Empty(t, w.sent)
}

func TestProducer_Write_PropagatesError(t *testing.T) {
	wantErr := fmt.Errorf("broker unreachable")
	w := &fakeWriter{err: wantErr}
	p := &Producer{writer: w}

	err := p.Write(context.Background(), []storage.Event{{Source: "a"}})
	require.ErrorIs(t, err, wantErr)
}

func TestProducer_Close(t *testing.T) {
	w := &fakeWriter{}
	p := &Producer{writer: w}

	require.NoError(t, p.Close())
	require.True(t, w.closed)
}

func TestNewProducer_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ProducerConfig
		wantErr bool
	}{
		{name: "Brokers пуст", cfg: ProducerConfig{Topic: "t"}, wantErr: true},
		{name: "Topic пуст", cfg: ProducerConfig{Brokers: []string{"localhost:9092"}}, wantErr: true},
		{name: "валидная конфигурация", cfg: ProducerConfig{Brokers: []string{"localhost:9092"}, Topic: "t"}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProducer(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, p)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, p)
			require.NoError(t, p.Close())
		})
	}
}

// TestNewProducer_BatchTimeoutDefault фиксирует фикс реальной проблемы,
// пойманной на интеграционном прогоне: дефолт BatchTimeout самого kafka-go
// (1с) делает каждый синхронный Write с малым числом сообщений медленным
// почти на секунду — NewProducer должен подставлять свой маленький дефолт,
// если BatchTimeout не задан явно.
func TestNewProducer_BatchTimeoutDefault(t *testing.T) {
	p, err := NewProducer(ProducerConfig{Brokers: []string{"localhost:9092"}, Topic: "t"})
	require.NoError(t, err)
	defer p.Close()

	w, ok := p.writer.(*kafkago.Writer)
	require.True(t, ok)
	require.Equal(t, defaultBatchTimeout, w.BatchTimeout)
}

func TestNewProducer_CustomBatchTimeout(t *testing.T) {
	custom := 50 * time.Millisecond
	p, err := NewProducer(ProducerConfig{Brokers: []string{"localhost:9092"}, Topic: "t", BatchTimeout: custom})
	require.NoError(t, err)
	defer p.Close()

	w, ok := p.writer.(*kafkago.Writer)
	require.True(t, ok)
	require.Equal(t, custom, w.BatchTimeout)
}
