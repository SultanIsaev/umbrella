package netflow

import (
	"encoding/binary"
	"fmt"
)

// DecodeV5 разбирает бинарный пакет NetFlow v5 (Big-Endian) на заголовок и записи.
// Возвращает ошибку на усечённом пакете, неподдерживаемой версии или
// некорректном количестве записей в заголовке.
//
// Байты читаются вручную через encoding/binary.BigEndian.Uint*, а не через
// binary.Read(r, order, &struct) — см. комментарий над EncodeV5 про причину
// (reflection в binary.Read/Write аллоцирует временный буфер на каждый вызов).
func DecodeV5(data []byte) (Header, []Record, error) {
	if len(data) < headerSize {
		return Header{}, nil, fmt.Errorf("netflow: packet too short: %d bytes, need at least %d for header", len(data), headerSize)
	}

	header := getHeader(data[:headerSize])

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
		off := headerSize + i*recordSize
		records[i] = getRecord(data[off : off+recordSize])
	}
	return header, records, nil
}

func getHeader(b []byte) Header {
	return Header{
		Version:          binary.BigEndian.Uint16(b[0:2]),
		Count:            binary.BigEndian.Uint16(b[2:4]),
		SysUptime:        binary.BigEndian.Uint32(b[4:8]),
		UnixSecs:         binary.BigEndian.Uint32(b[8:12]),
		UnixNsecs:        binary.BigEndian.Uint32(b[12:16]),
		FlowSequence:     binary.BigEndian.Uint32(b[16:20]),
		EngineType:       b[20],
		EngineID:         b[21],
		SamplingInterval: binary.BigEndian.Uint16(b[22:24]),
	}
}

func getRecord(b []byte) Record {
	var r Record
	copy(r.SrcAddr[:], b[0:4])
	copy(r.DstAddr[:], b[4:8])
	copy(r.NextHop[:], b[8:12])
	r.InputIf = binary.BigEndian.Uint16(b[12:14])
	r.OutputIf = binary.BigEndian.Uint16(b[14:16])
	r.Packets = binary.BigEndian.Uint32(b[16:20])
	r.Bytes = binary.BigEndian.Uint32(b[20:24])
	r.First = binary.BigEndian.Uint32(b[24:28])
	r.Last = binary.BigEndian.Uint32(b[28:32])
	r.SrcPort = binary.BigEndian.Uint16(b[32:34])
	r.DstPort = binary.BigEndian.Uint16(b[34:36])
	r.Pad1 = b[36]
	r.TCPFlags = b[37]
	r.Prot = b[38]
	r.ToS = b[39]
	r.SrcAS = binary.BigEndian.Uint16(b[40:42])
	r.DstAS = binary.BigEndian.Uint16(b[42:44])
	r.SrcMask = b[44]
	r.DstMask = b[45]
	r.Pad2 = binary.BigEndian.Uint16(b[46:48])
	return r
}
