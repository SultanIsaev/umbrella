package pipeline

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/SultanIsaev/umbrella/internal/netflow"
)

type Result struct {
	Header  netflow.Header
	Records []netflow.Record
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
