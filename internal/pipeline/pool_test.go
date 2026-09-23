package pipeline

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/SultanIsaev/umbrella/internal/ingest"
	"github.com/SultanIsaev/umbrella/internal/ipfix"
	"github.com/SultanIsaev/umbrella/internal/netflow"
	"github.com/SultanIsaev/umbrella/internal/storage"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// exporterA — фиксированный адрес отправителя для тестов, которым он
// безразличен (всё, кроме v9/IPFIX): важно только то, что он один и тот же
// на всём протяжении теста, чтобы попадать в один и тот же ключ кэша
// шаблонов.
var exporterA = netip.MustParseAddrPort("10.1.1.1:12345")

func TestFormatIPv4(t *testing.T) {
	tests := []struct {
		name string
		ip   [4]byte
		want string
	}{
		{name: "все нули", ip: [4]byte{0, 0, 0, 0}, want: "0.0.0.0"},
		{name: "максимум", ip: [4]byte{255, 255, 255, 255}, want: "255.255.255.255"},
		{name: "смешанные разрядности", ip: [4]byte{10, 0, 5, 123}, want: "10.0.5.123"},
		{name: "однозначные во всех октетах", ip: [4]byte{1, 2, 3, 4}, want: "1.2.3.4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, formatIPv4(tt.ip))
		})
	}
}

func TestNetflowV5Result_Events(t *testing.T) {
	r := NetflowV5Result{
		Header: netflow.Header{UnixSecs: 1_700_000_000, Count: 2},
		Records: []netflow.Record{
			{
				SrcAddr: [4]byte{10, 0, 0, 1}, DstAddr: [4]byte{8, 8, 8, 8},
				SrcPort: 44332, DstPort: 443, Prot: 6, Packets: 10, Bytes: 1500,
			},
			{
				SrcAddr: [4]byte{192, 168, 1, 1}, DstAddr: [4]byte{1, 1, 1, 1},
				SrcPort: 53, DstPort: 12345, Prot: 17, Packets: 1, Bytes: 64,
			},
		},
	}

	events := r.Events()
	require.Len(t, events, 2)

	require.Equal(t, int64(1_700_000_000), events[0].Timestamp)
	require.Equal(t, "netflow5", events[0].Source)
	require.Equal(t, storage.Fields{
		{Key: "src_addr", Value: "10.0.0.1"},
		{Key: "dst_addr", Value: "8.8.8.8"},
		{Key: "src_port", Value: uint16(44332)},
		{Key: "dst_port", Value: uint16(443)},
		{Key: "protocol", Value: uint8(6)},
		{Key: "packets", Value: uint32(10)},
		{Key: "bytes", Value: uint32(1500)},
	}, events[0].Fields)

	require.Equal(t, storage.Fields{
		{Key: "src_addr", Value: "192.168.1.1"},
		{Key: "dst_addr", Value: "1.1.1.1"},
		{Key: "src_port", Value: uint16(53)},
		{Key: "dst_port", Value: uint16(12345)},
		{Key: "protocol", Value: uint8(17)},
		{Key: "packets", Value: uint32(1)},
		{Key: "bytes", Value: uint32(64)},
	}, events[1].Fields)
}

// TestNetflowV9Result_Events проверяет, что поля идут в детерминированном
// порядке (по возрастанию типа) вне зависимости от порядка обхода исходной
// map[uint16][]byte — без этого тест был бы флаки.
func TestNetflowV9Result_Events(t *testing.T) {
	r := NetflowV9Result{
		Header: netflow.HeaderV9{UnixSecs: 1_700_000_000},
		Records: []netflow.RecordV9{
			{
				TemplateID: 256,
				Fields: map[uint16][]byte{
					8:  {10, 0, 0, 1},
					12: {8, 8, 8, 8},
					1:  {0, 0, 0, 100},
				},
			},
		},
	}

	events := r.Events()
	require.Len(t, events, 1)
	require.Equal(t, int64(1_700_000_000), events[0].Timestamp)
	require.Equal(t, "netflow9", events[0].Source)
	require.Equal(t, storage.Fields{
		{Key: "field_1", Value: "00000064"},
		{Key: "field_8", Value: "0a000001"},
		{Key: "field_12", Value: "08080808"},
	}, events[0].Fields)
}

// TestIPFIXResult_Events проверяет и детерминированный порядок, и то, что
// vendor-specific Information Element (EnterpriseNumber != 0) получают
// отдельный формат имени.
func TestIPFIXResult_Events(t *testing.T) {
	r := IPFIXResult{
		Header: ipfix.Header{ExportTime: 1_700_000_100},
		Records: []ipfix.Record{
			{
				TemplateID: 300,
				Fields: map[ipfix.FieldKey][]byte{
					{ElementID: 8}:                      {10, 0, 0, 1},
					{ElementID: 12}:                     {8, 8, 8, 8},
					{EnterpriseNumber: 9, ElementID: 1}: {1},
				},
			},
		},
	}

	events := r.Events()
	require.Len(t, events, 1)
	require.Equal(t, int64(1_700_000_100), events[0].Timestamp)
	require.Equal(t, "ipfix", events[0].Source)
	require.Equal(t, storage.Fields{
		{Key: "ie_8", Value: "0a000001"},
		{Key: "ie_12", Value: "08080808"},
		{Key: "ie_9.1", Value: "01"},
	}, events[0].Fields)
}

// packet собирает ingest.Packet с заданным payload и фиксированным
// exporterA — используется везде, где адрес отправителя не варьируется в
// рамках теста.
func packet(data []byte) ingest.Packet {
	return ingest.Packet{Data: data, From: exporterA}
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
			in := make(chan ingest.Packet, total)
			for i := range tt.validCount {
				in <- packet(validPacket(t, uint16(i)))
			}
			for range tt.invalidCount {
				in <- packet(invalidPacket())
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

// TestPool_Dispatch проверяет диспетчеризацию по версии протокола: NetFlow
// v9 и IPFIX должны декодироваться через соответствующий TemplateCache (а не
// молча попадать в invalid), а неизвестная версия и слишком короткий пакет —
// отбраковываться.
func TestPool_Dispatch(t *testing.T) {
	v9Header, v9Records := []byte{0x00, 0x09, 0x00, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte(nil)
	_ = v9Records

	pool, err := New(1)
	require.NoError(t, err)

	// NetFlow v9 без единого Template FlowSet: пакет структурно валиден
	// (Count=1 FlowSet, но сам FlowSet будет прочитан ниже), поэтому просто
	// проверим, что v9-пакет с валидным заголовком и без Data FlowSet'ов
	// декодируется без ошибки и не попадает в invalid.
	//
	// FlowSet: templateFlowSetID(0), длина 4 (только заголовок, без записей) —
	// минимальный структурно валидный FlowSet.
	v9Packet := append(append([]byte{}, v9Header...), 0x00, 0x00, 0x00, 0x04)

	results := make(chan Result, 3)
	in := make(chan ingest.Packet, 3)
	in <- ingest.Packet{Data: v9Packet, From: exporterA}
	in <- ingest.Packet{Data: []byte{0x00, 0x0a}, From: exporterA} // IPFIX, но короче headerSize -> error
	in <- ingest.Packet{Data: []byte{0x00, 0x63}, From: exporterA} // версия 99 — не поддерживается
	close(in)

	pool.Run(in, results)

	var got []Result
	for r := range results {
		got = append(got, r)
	}

	require.Len(t, got, 1, "только валидный v9-пакет должен дойти до results")
	_, isV9 := got[0].(NetflowV9Result)
	require.True(t, isV9, "результат должен быть NetflowV9Result")
	require.Equal(t, uint64(2), pool.Invalid())
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

	in := make(chan ingest.Packet)
	const total = 10
	results := make(chan Result, total)

	done := make(chan struct{})
	go func() {
		pool.Run(in, results)
		close(done)
	}()

	for i := range total {
		in <- packet(validPacket(t, uint16(i)))
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

	in := make(chan ingest.Packet)
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
