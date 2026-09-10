package netflow

import "testing"

func BenchmarkEncodeV5(b *testing.B) {
	header := sampleHeader(1)
	records := []Record{sampleRecord()}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := EncodeV5(header, records); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeV5_MaxRecords(b *testing.B) {
	records := make([]Record, maxRecordsPerPacket)
	for i := range records {
		records[i] = sampleRecord()
	}
	header := sampleHeader(maxRecordsPerPacket)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := EncodeV5(header, records); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeV5(b *testing.B) {
	data, err := EncodeV5(sampleHeader(1), []Record{sampleRecord()})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := DecodeV5(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeV5_MaxRecords(b *testing.B) {
	records := make([]Record, maxRecordsPerPacket)
	for i := range records {
		records[i] = sampleRecord()
	}
	data, err := EncodeV5(sampleHeader(maxRecordsPerPacket), records)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := DecodeV5(data); err != nil {
			b.Fatal(err)
		}
	}
}
