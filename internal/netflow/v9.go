package netflow

import "encoding/binary"

// headerV9Size — заголовок NetFlow v9: Version, Count, SysUptime, UnixSecs,
// PackageSequence, SourceID = 2+2+4+4+4+4 = 20 байт.
const headerV9Size = 20

// flowSetHeaderSize — FlowSet ID + Length, общие для любого FlowSet.
const flowSetHeaderSize = 4

const (
	templateFlowSetID             = 0
	optionsTemplateFlowSet        = 1
	minDataFlowSetID       uint16 = 256

	protocolVersionV9 = 9
)

// HeaderV9 — заголовок пакета NetFlow v9. В отличие от v5, Count здесь —
// число FlowSet'ов в пакете, а не число flow-записей: сколько именно записей
// внутри, зависит от шаблона каждого Data FlowSet и заранее не известно.
type HeaderV9 struct {
	Version         uint16
	Count           uint16
	SysUptime       uint32
	UnixSecs        uint32
	PackageSequence uint32
	SourceID        uint32 // observation domain экспортёра
}

// FieldSpec — один элемент шаблона: тип поля и его длина в байтах в
// Data-записях, использующих этот шаблон.
type FieldSpec struct {
	Type   uint16
	Length uint16
}

// TemplateV9 — разобранный Template Record: поля в порядке их следования в
// Data-записях с этим Template ID.
type TemplateV9 struct {
	ID     uint16
	Fields []FieldSpec
}

// recordSize — суммарная длина одной Data-записи по этому шаблону.
func (t TemplateV9) recordSize() int {
	n := 0
	for _, f := range t.Fields {
		n += int(f.Length)
	}
	return n
}

// RecordV9 — одна разобранная Data-запись: сырые байты каждого поля по его
// типу (взятому из шаблона). Семантическая интерпретация конкретных типов
// полей (IP-адрес, порт, счётчик — их 100+ стандартных в NetFlow v9)
// оставлена потребителю; этот шаг — только template-based разбор структуры.
type RecordV9 struct {
	TemplateID uint16
	Fields     map[uint16][]byte
}

func getHeaderV9(b []byte) HeaderV9 {
	return HeaderV9{
		Version:         binary.BigEndian.Uint16(b[0:2]),
		Count:           binary.BigEndian.Uint16(b[2:4]),
		SysUptime:       binary.BigEndian.Uint32(b[4:8]),
		UnixSecs:        binary.BigEndian.Uint32(b[8:12]),
		PackageSequence: binary.BigEndian.Uint32(b[12:16]),
		SourceID:        binary.BigEndian.Uint32(b[16:20]),
	}
}
