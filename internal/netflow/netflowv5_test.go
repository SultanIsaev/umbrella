package netflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureDir указывает на test/testdata/netflow относительно этого пакета
// (internal/netflow) — go test запускается с рабочей директорией пакета.
var fixtureDir = filepath.Join("..", "..", "test", "testdata", "netflow")

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	require.NoError(t, err)
	return data
}

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
	tests := []struct {
		name    string
		fixture string
	}{
		{"header too short", "invalid_header_too_short.bin"},
		{"truncated record", "invalid_truncated_record.bin"},
		{"wrong version", "invalid_wrong_version.bin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := readFixture(t, tt.fixture)
			_, _, err := DecodeV5(data)
			require.Error(t, err)
		})
	}

	t.Run("empty packet", func(t *testing.T) {
		_, _, err := DecodeV5(nil)
		require.Error(t, err)
	})
}

// TestDecodeV5_Fixtures прогоняет весь корпус test/testdata/netflow: каждый
// файл по имени объявляет, чего от него ждать (valid_*/invalid_*), поэтому
// новый файл, добавленный в корпус позже, подхватится автоматически без
// правки кода теста. Тот же корпус пригодится как seed для go test -fuzz.
func TestDecodeV5_Fixtures(t *testing.T) {
	entries, err := os.ReadDir(fixtureDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "ожидались бинарные фикстуры в %s", fixtureDir)

	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			data := readFixture(t, entry.Name())
			header, records, err := DecodeV5(data)

			switch {
			case strings.HasPrefix(entry.Name(), "valid_"):
				require.NoError(t, err)
				require.EqualValues(t, protocolVersion, header.Version)
				require.Len(t, records, int(header.Count))
			case strings.HasPrefix(entry.Name(), "invalid_"):
				require.Error(t, err)
			default:
				t.Fatalf("имя фикстуры %q должно начинаться с valid_ или invalid_", entry.Name())
			}
		})
	}
}

func TestEncodeV5_WireLayout(t *testing.T) {
	data, err := EncodeV5(sampleHeader(1), []Record{sampleRecord()})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), 2)
	require.Equal(t, []byte{0x00, 0x05}, data[0:2], "Version должен быть 5 в Big-Endian на первых двух байтах")
}
