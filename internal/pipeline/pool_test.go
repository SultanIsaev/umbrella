package pipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/SultanIsaev/umbrella/internal/netflow"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// validPacket собирает синтетический, но валидный NetFlow v5 пакет с одной
// записью — DecodeV5 должен разобрать его без ошибок.
func validPacket(t *testing.T, srcPort uint16) []byte {
	t.Helper()

	header := netflow.Header{Version: 5, Count: 1}
	records := []netflow.Record{{SrcPort: srcPort}}

	data, err := netflow.EncodeV5(header, records)
	require.NoError(t, err)
	return data
}

// invalidPacket короче headerSize — DecodeV5 гарантированно вернёт ошибку
// "packet too short", ещё до проверки версии/количества записей.
func invalidPacket() []byte {
	return []byte{0x01, 0x02, 0x03}
}

// TestPool_FanOut проверяет, что Pool корректно разбирает валидные пакеты и
// отбраковывает битые независимо от числа воркеров.
//
// pool.Run вызывается синхронно (не в отдельной горутине) и блокируется, пока
// все воркеры не остановятся: он сам ждёт остановки писателей через
// sync.WaitGroup внутри и закрывает results только после этого.
//
// results делаем буферизованным на wantValid элементов, чтобы
// воркерам было куда писать без отдельного читателя,
// работающего параллельно с Run — иначе при wantValid > 0 и небуферизованном results воркеры бы
// заблокировались на первой же отправке, а некому было бы её вычитать
// одновременно с ещё выполняющимся Run.
func TestPool_FanOut(t *testing.T) {
	tests := []struct {
		name         string
		workers      int
		validCount   int
		invalidCount int
	}{
		{
			name:       "один воркер, только валидные",
			workers:    1,
			validCount: 10,
		},
		{
			name:         "несколько воркеров, смесь валидных и битых",
			workers:      4,
			validCount:   20,
			invalidCount: 5,
		},
		{
			name:         "воркеров больше, чем пакетов",
			workers:      8,
			validCount:   3,
			invalidCount: 2,
		},
		{
			name:         "только битые пакеты",
			workers:      3,
			invalidCount: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := New(tt.workers)
			require.NoError(t, err)

			total := tt.validCount + tt.invalidCount
			in := make(chan []byte, total)
			for i := range tt.validCount {
				in <- validPacket(t, uint16(i))
			}
			for range tt.invalidCount {
				in <- invalidPacket()
			}
			close(in)

			results := make(chan Result, tt.validCount)

			pool.Run(in, results)

			var got []Result
			for r := range results {
				got = append(got, r)
			}

			require.Len(t, got, tt.validCount)
			require.Equal(t, uint64(tt.invalidCount), pool.Invalid())
		})
	}
}

// TestPool_GracefulShutdown проверяет две стороны остановки: пул не должен
// завершаться, пока in не закрыт (даже если все отправленные пакеты уже
// разобраны и воркеры простаивают в ожидании следующих), и обязан
// завершиться и закрыть results после закрытия in.
//
// in — небуферизованный: каждая отправка блокируется, пока какой-то воркер
// её не заберёт, поэтому к моменту, когда цикл отправки заканчивается, все
// пакеты уже гарантированно вычитаны из in (хотя не обязательно ещё
// дописаны в results). pool.Run при этом не может завершиться раньше
// закрытия in в принципе — это не гонка с таймингом, а прямое следствие
// того, что каждый воркер сидит в for range in и не выйдет из цикла, пока
// канал не закрыт, — поэтому проверка через select+default детерминирована.
func TestPool_GracefulShutdown(t *testing.T) {
	pool, err := New(4)
	require.NoError(t, err)

	in := make(chan []byte)
	const total = 10
	results := make(chan Result, total)

	done := make(chan struct{})
	go func() {
		pool.Run(in, results)
		close(done)
	}()

	for i := range total {
		in <- validPacket(t, uint16(i))
	}

	select {
	case <-done:
		t.Fatal("pool.Run завершился до закрытия in")
	default:
	}

	close(in)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool.Run не завершился после закрытия in")
	}

	var got []Result
	for r := range results {
		got = append(got, r)
	}
	require.Len(t, got, total)

	_, ok := <-results
	require.False(t, ok, "results должен быть закрыт после возврата pool.Run")
}

// TestPool_EmptyInput проверяет пустой поток: in закрывается сразу, ничего в него не отправляя.
func TestPool_EmptyInput(t *testing.T) {
	pool, err := New(4)
	require.NoError(t, err)

	in := make(chan []byte)
	close(in)

	results := make(chan Result)

	done := make(chan struct{})
	go func() {
		pool.Run(in, results)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool.Run не завершился на пустом входе")
	}

	_, ok := <-results
	require.False(t, ok, "results должен быть закрыт даже без единого пакета")
	require.Equal(t, uint64(0), pool.Invalid())
}

// TestNew проверяет валидацию числа воркеров.
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		workers int
		wantErr bool
	}{
		{name: "ноль воркеров", workers: 0, wantErr: true},
		{name: "отрицательное число воркеров", workers: -1, wantErr: true},
		{name: "один воркер", workers: 1},
		{name: "несколько воркеров", workers: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := New(tt.workers)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, pool)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, pool)
		})
	}
}
