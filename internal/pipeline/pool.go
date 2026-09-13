package pipeline

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/SultanIsaev/umbrella/internal/netflow"
	"github.com/SultanIsaev/umbrella/internal/storage"
)

type Result struct {
	Header  netflow.Header
	Records []netflow.Record
}

// Events превращает один разобранный NetFlow-пакет в набор нормализованных
// событий — по одному на каждый Record (это и есть единица телеметрии, а не пакет целиком).
//
// Timestamp — приближение через Header.UnixSecs (время экспорта пакета),
// не точное время потока. Точный расчёт требует пересчёта через дельты
// SysUptime/First/Last — оставлено на потом, не блокирует M4.
//
// Source — временная заглушка-тег протокола: ingest.Listener пока не
// прокидывает адрес отправителя через пайплайн (ReadFromUDP его отбрасывает),
// так что настоящий IP экспортёра сюда пока не попадает.
func (r Result) Events() []storage.Event {
	events := make([]storage.Event, len(r.Records))
	for i, rec := range r.Records {
		events[i] = storage.Event{
			Timestamp: int64(r.Header.UnixSecs),
			Source:    "netflow5",
			Fields: map[string]any{
				"src_addr": formatIPv4(rec.SrcAddr),
				"dst_addr": formatIPv4(rec.DstAddr),
				"src_port": rec.SrcPort,
				"dst_port": rec.DstPort,
				"protocol": rec.Prot,
				"packets":  rec.Packets,
				"bytes":    rec.Bytes,
			},
		}
	}
	return events
}

// formatIPv4 форматирует 4 байта в точечно-десятичную нотацию
// ("10.0.0.1") без net.IP.String(): тот делает общую IPv4/IPv6
// детекцию и форматирование под оба случая, чего здесь не нужно — тип
// адреса уже точно известен ([4]byte из NetFlow v5 Record). Профилирование
// под loadgen (docs/benchmarks.md) показало net.IP.String() заметной
// частью аллокаций на этом горячем пути.
func formatIPv4(ip [4]byte) string {
	var buf [15]byte // "255.255.255.255" — максимум 15 символов
	n := 0
	for i, b := range ip {
		if i > 0 {
			buf[n] = '.'
			n++
		}
		n += appendDecimalByte(buf[n:], b)
	}
	return string(buf[:n])
}

// appendDecimalByte пишет десятичное представление b в dst (без ведущих
// нулей) и возвращает число записанных байт. dst должен вмещать минимум 3
// байта — вызывающий код (formatIPv4) это гарантирует.
func appendDecimalByte(dst []byte, b byte) int {
	switch {
	case b >= 100:
		dst[0] = '0' + b/100
		dst[1] = '0' + b/10%10
		dst[2] = '0' + b%10
		return 3
	case b >= 10:
		dst[0] = '0' + b/10
		dst[1] = '0' + b%10
		return 2
	default:
		dst[0] = '0' + b
		return 1
	}
}

type Pool struct {
	workers int
	invalid atomic.Uint64
}

func New(workers int) (*Pool, error) {
	if workers <= 0 {
		return nil, fmt.Errorf("invalid count of workers: %d", workers)
	}
	return &Pool{
		workers: workers,
	}, nil
}

func (p *Pool) Run(in <-chan []byte, results chan<- Result) {
	var wg sync.WaitGroup
	wg.Add(p.workers)
	for range p.workers {
		go func() {
			defer wg.Done()
			for pkt := range in {
				header, records, err := netflow.DecodeV5(pkt)
				if err != nil {
					p.invalid.Add(1)
					continue
				}
				results <- Result{Header: header, Records: records}
			}
		}()
	}
	wg.Wait()
	close(results) // Pool - оркестратор для results, закрывает только он и только тут
}

func (p *Pool) Invalid() uint64 {
	return p.invalid.Load()
}
