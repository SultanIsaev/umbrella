package netflow

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// DecodeV5 разбирает бинарный пакет NetFlow v5 (Big-Endian) на заголовок и записи.
// Возвращает ошибку на усечённом пакете, неподдерживаемой версии или
// некорректном количестве записей в заголовке.
func DecodeV5(data []byte) (Header, []Record, error) {
	if len(data) < headerSize {
		return Header{}, nil, fmt.Errorf("netflow: packet too short: %d bytes, need at least %d for header", len(data), headerSize)
	}

	r := bytes.NewReader(data)
	var header Header
	if err := binary.Read(r, binary.BigEndian, &header); err != nil {
		return Header{}, nil, fmt.Errorf("netflow: decode header: %w", err)
	}

	if header.Version != protocolVersion {
		return Header{}, nil, fmt.Errorf("netflow: unsupported version %d, expected %d", header.Version, protocolVersion)
	}
	if header.Count == 0 || int(header.Count) > maxRecordsPerPacket {
		return Header{}, nil, fmt.Errorf("netflow: invalid record count %d, must be in [1,%d]", header.Count, maxRecordsPerPacket)
	}

	want := headerSize + int(header.Count)*recordSize
	if len(data) < want {
		return Header{}, nil, fmt.Errorf("netflow: packet truncated: have %d bytes, want %d for %d record(s)", len(data), want, header.Count)
	}

	records := make([]Record, header.Count)
	for i := range records {
		if err := binary.Read(r, binary.BigEndian, &records[i]); err != nil {
			return Header{}, nil, fmt.Errorf("netflow: decode record %d: %w", i, err)
		}
	}
	return header, records, nil
}
