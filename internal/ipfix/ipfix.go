package ipfix

import "encoding/binary"

// headerSize — заголовок IPFIX Message: Version, Length, Export Time,
// Sequence Number, Observation Domain ID = 2+2+4+4+4 = 16 байт.
const headerSize = 16

// setHeaderSize — Set ID + Length, общие для любого Set.
const setHeaderSize = 4

const (
	templateSetID               = 2
	optionsTemplateSetID        = 3
	minDataSetID         uint16 = 256

	protocolVersion = 10

	// variableLength — специальное значение Field Length в Template
	// Record, означающее, что реальная длина поля в Data Record не
	// фиксирована и кодируется отдельно перед самим значением (см.
	// decodeOneRecord и RFC 7011 §7).
	variableLength uint16 = 65535

	// enterpriseBit — старший бит Information Element ID в Field Specifier:
	// если установлен, за (ID без этого бита, Length) следует ещё 4 байта
	// Enterprise Number — приватное пространство ID конкретного вендора.
	enterpriseBit uint16 = 0x8000
)

// Header — заголовок IPFIX Message. Length — длина всего сообщения включая
// заголовок (в отличие от NetFlow v9, где Count — число FlowSet'ов, а не
// байт).
type Header struct {
	Version             uint16
	Length              uint16
	ExportTime          uint32
	SequenceNumber      uint32
	ObservationDomainID uint32
}

// FieldKey идентифицирует один Information Element. Для стандартных IE
// EnterpriseNumber == 0 и идентификатором служит только ElementID; для
// vendor-specific IE один и тот же ElementID у разных вендоров означает
// разные вещи, поэтому EnterpriseNumber — часть идентичности поля, не
// побочная метаданная.
type FieldKey struct {
	EnterpriseNumber uint32
	ElementID        uint16
}

// FieldSpec — один элемент шаблона: идентичность поля и его длина в Data
// Record. Length == variableLength означает переменную длину (см. пакетный
// комментарий у variableLength).
type FieldSpec struct {
	Key    FieldKey
	Length uint16
}

// Template — разобранный Template Record.
type Template struct {
	ID     uint16
	Fields []FieldSpec
}

// Record — одна разобранная Data Record. Как и в netflow.RecordV9,
// семантическая интерпретация конкретных Information Element (их сотни в
// стандартном IANA-реестре плюс vendor-specific) оставлена потребителю —
// этот пакет разбирает только структуру по шаблону.
type Record struct {
	TemplateID uint16
	Fields     map[FieldKey][]byte
}

func getHeader(b []byte) Header {
	return Header{
		Version:             binary.BigEndian.Uint16(b[0:2]),
		Length:              binary.BigEndian.Uint16(b[2:4]),
		ExportTime:          binary.BigEndian.Uint32(b[4:8]),
		SequenceNumber:      binary.BigEndian.Uint32(b[8:12]),
		ObservationDomainID: binary.BigEndian.Uint32(b[12:16]),
	}
}
