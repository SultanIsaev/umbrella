package netflow

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func sampleRecord() Record {
	return Record{
		SrcAddr:  [4]byte{192, 168, 1, 50},
		DstAddr:  [4]byte{8, 8, 8, 8},
		NextHop:  [4]byte{192, 168, 1, 1},
		InputIf:  1,
		OutputIf: 2,
		Packets:  10,
		Bytes:    1500,
		First:    3595000,
		Last:     3599000,
		SrcPort:  44332,
		DstPort:  443,
		TCPFlags: 0x10,
		Prot:     6,
		ToS:      0,
		SrcAS:    0,
		DstAS:    15169,
		SrcMask:  24,
		DstMask:  32,
	}
}

func sampleHeader(count uint16) Header {
	return Header{
		Version:          protocolVersion,
		Count:            count,
		SysUptime:        3600000,
		UnixSecs:         1_700_000_000,
		UnixNsecs:        123456,
		FlowSequence:     42,
		EngineType:       0,
		EngineID:         0,
		SamplingInterval: 0,
	}
}

func TestEncodeDecodeV5_RoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		numRecords int
	}{
		{"single record", 1},
		{"multiple records", 3},
		{"max records", maxRecordsPerPacket},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := make([]Record, tt.numRecords)
			for i := range records {
				records[i] = sampleRecord()
				records[i].SrcPort = uint16(1000 + i)
			}
			header := sampleHeader(uint16(tt.numRecords))

			data, err := EncodeV5(header, records)
			require.NoError(t, err)
			require.Len(t, data, headerSize+tt.numRecords*recordSize)

			gotHeader, gotRecords, err := DecodeV5(data)
			require.NoError(t, err)
			require.Equal(t, header, gotHeader)
			require.Equal(t, records, gotRecords)
		})
	}
}

func TestEncodeV5_Errors(t *testing.T) {
	tests := []struct {
		name    string
		header  Header
		records []Record
	}{
		{"no records", sampleHeader(0), nil},
		{"too many records", sampleHeader(maxRecordsPerPacket + 1), make([]Record, maxRecordsPerPacket+1)},
		{"count mismatch", sampleHeader(2), []Record{sampleRecord()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := EncodeV5(tt.header, tt.records)
			require.Error(t, err)
		})
	}
}

func TestDecodeV5_Errors(t *testing.T) {
	valid, err := EncodeV5(sampleHeader(1), []Record{sampleRecord()})
	require.NoError(t, err)

	tests := []struct {
		name string
		data []byte
	}{
		{"empty packet", nil},
		{"header too short", valid[:headerSize-1]},
		{"truncated record", valid[:headerSize+recordSize-1]},
		{"wrong version", func() []byte {
			cp := append([]byte(nil), valid...)
			cp[1] = 9 // Version — big-endian uint16, младший байт на смещении 1
			return cp
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := DecodeV5(tt.data)
			require.Error(t, err)
		})
	}
}

func TestEncodeV5_WireLayout(t *testing.T) {
	data, err := EncodeV5(sampleHeader(1), []Record{sampleRecord()})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), 2)
	require.Equal(t, []byte{0x00, 0x05}, data[0:2], "Version должен быть 5 в Big-Endian на первых двух байтах")
}
