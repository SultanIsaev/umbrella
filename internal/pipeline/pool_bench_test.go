package pipeline

import (
	"testing"

	"github.com/SultanIsaev/umbrella/internal/netflow"
)

func sampleResult(recordCount int) Result {
	records := make([]netflow.Record, recordCount)
	for i := range records {
		records[i] = netflow.Record{
			SrcAddr: [4]byte{10, 0, 0, byte(i % 256)},
			DstAddr: [4]byte{192, 168, byte(i / 256), byte(i % 256)},
			SrcPort: uint16(1024 + i),
			DstPort: 80,
			Packets: 1,
			Bytes:   64,
			Prot:    6,
		}
	}
	return Result{
		Header:  netflow.Header{UnixSecs: 1_700_000_000, Count: uint16(recordCount)},
		Records: records,
	}
}

func BenchmarkResultEvents(b *testing.B) {
	r := sampleResult(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Events()
	}
}

func BenchmarkResultEvents_MaxRecords(b *testing.B) {
	r := sampleResult(30) // maxRecordsPerPacket в internal/netflow
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Events()
	}
}
