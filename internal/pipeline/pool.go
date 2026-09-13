package pipeline

import (
	"fmt"
	"net"
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
				"src_addr": net.IP(rec.SrcAddr[:]).String(),
				"dst_addr": net.IP(rec.DstAddr[:]).String(),
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
