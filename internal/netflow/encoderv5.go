package netflow

import (
	"encoding/binary"
	"fmt"
)

// Header представляет заголовок NetFlow v5 (ровно 24 байта)
type Header struct {
	Version          uint16 // Версия протокола (всегда 5)
	Count            uint16 // Количество записей в пакете (1-30)
	SysUptime        uint32 // Время работы устройства в миллисекундах с момента загрузки
	UnixSecs         uint32 // Текущее время Epoch в секундах
	UnixNsecs        uint32 // Остаток текущего времени в наносекундах
	FlowSequence     uint32 // Порядковый номер отправленных потоков (счетчик всех flows)
	EngineType       uint8  // Тип переключающего движка (обычно 0)
	EngineID         uint8  // ID движка (обычно 0)
	SamplingInterval uint16 // Интервал дискретизации (обычно 0)
}

// Record представляет одну запись потока NetFlow v5 (ровно 48 байт)
type Record struct {
	SrcAddr  [4]byte // IP-адрес источника
	DstAddr  [4]byte // IP-адрес назначения
	NextHop  [4]byte // IP следующего хопа (маршрутизатора)
	InputIf  uint16  // Индекс входящего интерфейса (SNMP ifIndex)
	OutputIf uint16  // Индекс исходящего интерфейса (SNMP ifIndex)
	Packets  uint32  // Количество пакетов в потоке
	Bytes    uint32  // Общее количество байт в потоке
	First    uint32  // SysUptime на момент появления первого пакета потока
	Last     uint32  // SysUptime на момент появления последнего пакета потока
	SrcPort  uint16  // TCP/UDP порт источника
	DstPort  uint16  // TCP/UDP порт назначения
	Pad1     uint8   // Выравнивание (всегда 0)
	TCPFlags uint8   // Набор TCP-флагов (OR всех пакетов потока)
	Prot     uint8   // IP-протокол (например, 6 для TCP, 17 для UDP)
	ToS      uint8   // Type of Service (IP ToS)
	SrcAS    uint16  // Автономная система источника (BGP AS)
	DstAS    uint16  // Автономная система назначения (BGP AS)
	SrcMask  uint8   // Маска подсети источника (CIDR префикс)
	DstMask  uint8   // Маска подсети назначения (CIDR префикс)
	Pad2     uint16  // Выравнивание (всегда 0)
}

const (
	protocolVersion     = 5
	maxRecordsPerPacket = 30
)

var (
	headerSize = binary.Size(Header{})
	recordSize = binary.Size(Record{})
)

// EncodeV5 сериализует заголовок и записи NetFlow v5 в бинарный вид (Big-Endian).
// header.Count должен совпадать с len(records);
// допустимо от 1 до 30 записей на пакет (ограничение протокола NetFlow v5).
//
// Байты пакуются вручную через encoding/binary.BigEndian.PutUint*, а не через
// binary.Write(w, order, struct) — последний на каждый struct-аргумент идёт
// через reflection и аллоцирует отдельный временный буфер (см. docs/benchmarks.md,
// запись от переезда на ручную упаковку: было 33 alloc/op на 30 записей, стало 1).
func EncodeV5(header Header, records []Record) ([]byte, error) {
	if len(records) == 0 || len(records) > maxRecordsPerPacket {
		return nil, fmt.Errorf("netflow: records count must be in [1,%d], got %d", maxRecordsPerPacket, len(records))
	}
	if int(header.Count) != len(records) {
		return nil, fmt.Errorf("netflow: header.Count=%d does not match len(records)=%d", header.Count, len(records))
	}

	data := make([]byte, headerSize+len(records)*recordSize)
	putHeader(data[:headerSize], header)
	for i, record := range records {
		off := headerSize + i*recordSize
		putRecord(data[off:off+recordSize], record)
	}
	return data, nil
}

func putHeader(b []byte, h Header) {
	binary.BigEndian.PutUint16(b[0:2], h.Version)
	binary.BigEndian.PutUint16(b[2:4], h.Count)
	binary.BigEndian.PutUint32(b[4:8], h.SysUptime)
	binary.BigEndian.PutUint32(b[8:12], h.UnixSecs)
	binary.BigEndian.PutUint32(b[12:16], h.UnixNsecs)
	binary.BigEndian.PutUint32(b[16:20], h.FlowSequence)
	b[20] = h.EngineType
	b[21] = h.EngineID
	binary.BigEndian.PutUint16(b[22:24], h.SamplingInterval)
}

func putRecord(b []byte, r Record) {
	copy(b[0:4], r.SrcAddr[:])
	copy(b[4:8], r.DstAddr[:])
	copy(b[8:12], r.NextHop[:])
	binary.BigEndian.PutUint16(b[12:14], r.InputIf)
	binary.BigEndian.PutUint16(b[14:16], r.OutputIf)
	binary.BigEndian.PutUint32(b[16:20], r.Packets)
	binary.BigEndian.PutUint32(b[20:24], r.Bytes)
	binary.BigEndian.PutUint32(b[24:28], r.First)
	binary.BigEndian.PutUint32(b[28:32], r.Last)
	binary.BigEndian.PutUint16(b[32:34], r.SrcPort)
	binary.BigEndian.PutUint16(b[34:36], r.DstPort)
	b[36] = r.Pad1
	b[37] = r.TCPFlags
	b[38] = r.Prot
	b[39] = r.ToS
	binary.BigEndian.PutUint16(b[40:42], r.SrcAS)
	binary.BigEndian.PutUint16(b[42:44], r.DstAS)
	b[44] = r.SrcMask
	b[45] = r.DstMask
	binary.BigEndian.PutUint16(b[46:48], r.Pad2)
}
