package ingest

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestHappyPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener, err := NewListener("127.0.0.1:0")
	require.NoError(t, err)

	out := make(chan []byte, 1)

	// 1. Запуск Run
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- listener.Run(ctx, out)
	}()

	// 2. Отправка пакета
	conn, err := net.Dial("udp", listener.LocalAddr().String())
	require.NoError(t, err)
	defer conn.Close()

	payload := []byte("hello")
	_, err = conn.Write(payload)
	require.NoError(t, err)

	// 3. Ожидание данных из канала
	select {
	case v, ok := <-out:
		if !ok {
			t.Fatal("out unexpectedly closed")
		}

		require.Equal(t, payload, v)
	case <-time.After(2 * time.Second):
		t.Errorf("listener.Run execution timeout")
	}
	// 4. Останавливаем listener
	cancel()
	// 5. Ожидаем завершения горутины Run
	select {
	case err := <-runErrCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Errorf("listener.Run execution timeout")
	}

	require.Equal(t, uint64(1), listener.Received())
	require.Equal(t, uint64(0), listener.Dropped())
	require.Equal(t, uint64(0), listener.Truncated())
}

// TestDispatch — детерминированный unit-тест backpressure-решения: без
// сети, без горутин, без таймаутов. dispatch вызывается синхронно из
// тестовой горутины, поэтому исход (принят/дропнут) зависит только от
// ёмкости буфера канала на момент вызова — никакой гонки с таймингом
// читателя здесь нет и быть не может.
func TestDispatch(t *testing.T) {
	tests := []struct {
		name         string
		bufSize      int
		packets      int
		wantReceived uint64
		wantDropped  uint64
	}{
		{
			name:         "буфер вмещает все пакеты — дропов нет",
			bufSize:      3,
			packets:      3,
			wantReceived: 3,
			wantDropped:  0,
		},
		{
			name:         "буфер меньше числа пакетов — лишние дропаются",
			bufSize:      1,
			packets:      3,
			wantReceived: 3,
			wantDropped:  2,
		},
		{
			name:         "нулевой буфер без читателя — дропается всё",
			bufSize:      0,
			packets:      3,
			wantReceived: 3,
			wantDropped:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := NewListener("127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()

			out := make(chan []byte, tt.bufSize)

			for range tt.packets {
				listener.dispatch(out, []byte("hello"))
			}

			require.Equal(t, tt.wantReceived, listener.Received())
			require.Equal(t, tt.wantDropped, listener.Dropped())
		})
	}
}

// TestRunBackpressure — проверяет только то,
// что dispatch, что цикл чтения в Run действительно вызывает dispatch с
// декодированными данными (wiring), а не саму backpressure-логику — та уже
// исчерпывающе и детерминированно покрыта TestDispatch.
//
// out намеренно буферизован (ёмкость 1): Run читает и диспетчеризирует
// пакеты строго последовательно в одной горутине, поэтому первый пакет
// гарантированно попадёт в свободный буфер, а второй — гарантированно
// будет дропнут (буфер уже полон). Это не зависит от того, когда именно
// тест начнёт читать out, — в отличие от небуферизованного канала, здесь
// нет гонки "читатель уже запаркован в select или ещё нет". Единственный
// оставшийся источник недетерминизма — сама доставка UDP по loopback
func TestRunBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener, err := NewListener("127.0.0.1:0")
	require.NoError(t, err)

	out := make(chan []byte, 1)

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- listener.Run(ctx, out)
	}()

	conn, err := net.Dial("udp", listener.LocalAddr().String())
	require.NoError(t, err)
	defer conn.Close()

	payload := []byte("hello")
	const total uint64 = 2
	for range total {
		_, err = conn.Write(payload)
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		return listener.Received() == total
	}, 2*time.Second, 10*time.Millisecond, "listener did not process all packets")

	cancel()

	select {
	case err := <-runErrCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatalf("listener.Run execution timeout")
	}

	require.Equal(t, total-1, listener.Dropped())
	require.Equal(t, uint64(0), listener.Truncated())

	select {
	case v := <-out:
		require.Equal(t, payload, v)
	default:
		t.Fatal("expected one packet buffered in out")
	}
}

// TestTruncated проверяет ветку "возможно обрезанный пакет" в Run: датаграмма
// размером ровно bufferSize — единственный доступный без MSG_TRUNC признак
// обрезки (см. комментарий у соответствующей проверки в Run). Такой пакет
// должен уйти в метрику Truncated, а не в out,
// и не должен засчитываться в Received.
func TestTruncated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener, err := NewListener("127.0.0.1:0")
	require.NoError(t, err)

	out := make(chan []byte, 1)

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- listener.Run(ctx, out)
	}()

	// DialUDP вместо Dial: нужен доступ к SetWriteBuffer — дефолтный
	// SO_SNDBUF на Darwin меньше bufferSize, и без его расширения
	// Write максимальной датаграммы падает с "message too long" ещё
	// на стороне отправителя, до всякой отправки по сети.
	raddr := listener.LocalAddr().(*net.UDPAddr)
	conn, err := net.DialUDP("udp", nil, raddr)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetWriteBuffer(bufferSize))

	oversized := make([]byte, bufferSize)
	_, err = conn.Write(oversized)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return listener.Truncated() == 1
	}, 2*time.Second, 10*time.Millisecond, "listener did not observe truncated packet")

	cancel()

	select {
	case err := <-runErrCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatalf("listener.Run execution timeout")
	}

	require.Equal(t, uint64(0), listener.Received())
	require.Equal(t, uint64(0), listener.Dropped())

	select {
	case <-out:
		t.Fatal("truncated packet must not be forwarded to out")
	default:
	}
}

func TestRunErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener, err := NewListener("127.0.0.1:0")
	require.NoError(t, err)

	out := make(chan []byte, 1)

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- listener.Run(ctx, out)
	}()

	err = listener.Close()
	require.NoError(t, err)

	select {
	case runErr := <-runErrCh:
		require.Error(t, runErr)
		require.Contains(t, runErr.Error(), "udp listener read error")
		var opErr *net.OpError
		require.ErrorAs(t, runErr, &opErr)
		require.Equal(t, "read", opErr.Op)
		require.Equal(t, "udp", opErr.Net)

		require.Equal(t, uint64(0), listener.Received())
		require.Equal(t, uint64(0), listener.Dropped())
		require.Equal(t, uint64(0), listener.Truncated())
	case <-time.After(2 * time.Second):
		t.Fatalf("listener.Run execution timeout")
	}
}
