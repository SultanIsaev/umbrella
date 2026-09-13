package stub

import (
	"context"
	"fmt"
	"sync"

	"github.com/SultanIsaev/umbrella/internal/storage"
)

// MemoryStorage накапливает записанные события в памяти — без реального
// сохранения куда-либо. Предназначена для тестов пайплайна: позволяет
// проверить, что именно дошло до storage.Storage, не поднимая реальный backend.
type MemoryStorage struct {
	mu     sync.Mutex
	events []storage.Event
	closed bool
}

var _ storage.Storage = (*MemoryStorage)(nil)

// NewMemoryStorage создаёт пустое in-memory хранилище.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{}
}

func (s *MemoryStorage) Write(ctx context.Context, events []storage.Event) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("memory storage: write: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("memory storage: write after close")
	}

	s.events = append(s.events, events...)
	return nil
}

// Close останавливает хранилище: последующие Write вернут ошибку вместо
// молчаливой записи в уже "закрытое" хранилище.
func (s *MemoryStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	return nil
}

// Events возвращает копию всех накопленных событий — для проверок в тестах.
// Копия, а не внутренний срез напрямую, чтобы вызывающий код не мог
// случайно изменить состояние MemoryStorage.
func (s *MemoryStorage) Events() []storage.Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]storage.Event, len(s.events))
	copy(out, s.events)
	return out
}
