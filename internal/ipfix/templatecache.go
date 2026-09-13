package ipfix

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
)

// templateKey идентифицирует шаблон в разрезе конкретного экспортёра и его
// Observation Domain — как и в netflow.TemplateCache, один и тот же
// Template ID у разных экспортёров (или разных доменов одного устройства)
// может означать разные раскладки полей.
type templateKey struct {
	exporter   string
	domainID   uint32
	templateID uint16
}

// TemplateCache хранит шаблоны IPFIX, полученные из Template Set'ов, и
// использует их для разбора последующих Data Set'ов. Устроен по тому же
// принципу, что и netflow.TemplateCache (см. его doc-комментарий про
// причину стейтфулности) — безопасен для конкурентного использования.
type TemplateCache struct {
	mu        sync.RWMutex
	templates map[templateKey]Template

	unknownTemplate  atomic.Uint64
	malformedDataSet atomic.Uint64
}

// NewTemplateCache создаёт пустой кэш шаблонов.
func NewTemplateCache() *TemplateCache {
	return &TemplateCache{templates: make(map[templateKey]Template)}
}

// UnknownTemplate — сколько Data Set'ов пришлось дропнуть, потому что
// соответствующий шаблон ещё не известен ("data record раньше template") —
// ожидаемый кейс, не ошибка разбора.
func (c *TemplateCache) UnknownTemplate() uint64 {
	return c.unknownTemplate.Load()
}

// MalformedDataSet — сколько раз разбор Data Set'а на известном шаблоне
// оборвался из-за нехватки байт на очередную запись
// (см. decodeDataSet: разница с паддингом — эвристика по оставшимся байтам).
func (c *TemplateCache) MalformedDataSet() uint64 {
	return c.malformedDataSet.Load()
}

// Decode разбирает IPFIX Message: обновляет кэш шаблонов из Template Set'ов
// и декодирует Data Set'ы, для которых шаблон уже известен.
//
// exporterKey идентифицирует источник пакета — тот же паттерн и тот же
// известный пробел (адрес экспортёра пока не прокидывается через
// пайплайн), что и у netflow.TemplateCache.DecodeV9.
//
// Ошибка возвращается только при нарушении структуры сообщения. Data Set с
// неизвестным шаблоном — не ошибка (см. UnknownTemplate): сообщение
// разбирается дальше, такой Set просто пропускается по объявленной длине.
func (c *TemplateCache) Decode(exporterKey string, data []byte) (Header, []Record, error) {
	if len(data) < headerSize {
		return Header{}, nil, fmt.Errorf("ipfix: message too short: %d bytes, need at least %d for header", len(data), headerSize)
	}

	header := getHeader(data[:headerSize])
	if header.Version != protocolVersion {
		return Header{}, nil, fmt.Errorf("ipfix: unsupported version %d, expected %d", header.Version, protocolVersion)
	}
	if int(header.Length) != len(data) {
		return Header{}, nil, fmt.Errorf("ipfix: header.Length=%d does not match message size %d", header.Length, len(data))
	}

	remaining := data[headerSize:]
	var records []Record

	for len(remaining) >= setHeaderSize {
		setID := binary.BigEndian.Uint16(remaining[0:2])
		length := binary.BigEndian.Uint16(remaining[2:4])

		if int(length) < setHeaderSize {
			return Header{}, nil, fmt.Errorf("ipfix: set length %d shorter than set header (%d)", length, setHeaderSize)
		}
		if int(length) > len(remaining) {
			return Header{}, nil, fmt.Errorf("ipfix: set declares %d bytes, only %d remain in message", length, len(remaining))
		}

		body := remaining[setHeaderSize:length]
		remaining = remaining[length:]

		switch {
		case setID == templateSetID:
			if err := c.learnTemplates(exporterKey, header.ObservationDomainID, body); err != nil {
				return Header{}, nil, err
			}
		case setID == optionsTemplateSetID:
			// Options Template — вне рамок этого шага, безопасно
			// пропускаем: тело уже отброшено вместе с остальным Set'ом.
		case setID >= minDataSetID:
			if recs, ok := c.decodeDataSet(exporterKey, header.ObservationDomainID, setID, body); ok {
				records = append(records, recs...)
			}
		default:
			// Set ID 0,1,4-255 зарезервированы спецификацией и не должны
			// встречаться на практике — не роняем всё сообщение, пропускаем.
		}
	}
	// Меньше 4 байт, оставшихся в remaining — паддинг до конца сообщения,
	// не ошибка (уже гарантировано header.Length-проверкой выше, что общий размер согласован).

	return header, records, nil
}

// learnTemplates разбирает Template Set: может содержать несколько Template
// Record'ов подряд, вплоть до паддинга в конце Set'а. В отличие от NetFlow
// v9, каждый Field Specifier занимает 4 или 8 байт (+4, если установлен
// enterpriseBit), поэтому размер записи не известен заранее — разбирается поле за полем.
func (c *TemplateCache) learnTemplates(exporterKey string, domainID uint32, body []byte) error {
	for len(body) >= 4 {
		templateID := binary.BigEndian.Uint16(body[0:2])
		fieldCount := binary.BigEndian.Uint16(body[2:4])
		body = body[4:]

		fields := make([]FieldSpec, 0, fieldCount)
		for i := uint16(0); i < fieldCount; i++ {
			if len(body) < 4 {
				return fmt.Errorf("ipfix: template %d: not enough bytes for field specifier %d/%d", templateID, i+1, fieldCount)
			}
			rawID := binary.BigEndian.Uint16(body[0:2])
			length := binary.BigEndian.Uint16(body[2:4])
			body = body[4:]

			var key FieldKey
			if rawID&enterpriseBit != 0 {
				if len(body) < 4 {
					return fmt.Errorf("ipfix: template %d: not enough bytes for enterprise number of field %d/%d", templateID, i+1, fieldCount)
				}
				key.EnterpriseNumber = binary.BigEndian.Uint32(body[0:4])
				key.ElementID = rawID &^ enterpriseBit
				body = body[4:]
			} else {
				key.ElementID = rawID
			}
			fields = append(fields, FieldSpec{Key: key, Length: length})
		}

		key := templateKey{exporter: exporterKey, domainID: domainID, templateID: templateID}
		c.mu.Lock()
		c.templates[key] = Template{ID: templateID, Fields: fields}
		c.mu.Unlock()
	}
	// Меньше 4 байт, оставшихся в body — паддинг до конца Set'а, не ошибка.
	return nil
}

// decodeDataSet разбирает Data Set на отдельные записи по уже известному
// шаблону. ok=false означает только "шаблон не найден" (см. UnknownTemplate)
// — если шаблон найден, но часть данных не удалось разобрать (см.
// MalformedDataSet), уже успешно разобранные записи всё равно возвращаются
// с ok=true.
func (c *TemplateCache) decodeDataSet(exporterKey string, domainID uint32, templateID uint16, body []byte) ([]Record, bool) {
	key := templateKey{exporter: exporterKey, domainID: domainID, templateID: templateID}

	c.mu.RLock()
	tmpl, ok := c.templates[key]
	c.mu.RUnlock()

	if !ok {
		c.unknownTemplate.Add(1)
		return nil, false
	}

	var records []Record
	for len(body) > 0 {
		rec, consumed, ok := decodeOneRecord(tmpl, body)
		if !ok {
			// Паддинг осмыслен только ПОСЛЕ хотя бы одной успешно
			// разобранной записи (выравнивание Set'а до границы) — если не
			// удалось разобрать вообще ни одной записи, оставшиеся байты не
			// могут быть паддингом ни при каком их количестве, это либо
			// пустой (некорректный) Set, либо повреждённые данные.
			if len(records) == 0 || len(body) >= 4 {
				c.malformedDataSet.Add(1)
			}
			break
		}
		records = append(records, rec)
		body = body[consumed:]
	}
	return records, true
}

// decodeOneRecord декодирует ровно одну Data Record по шаблону tmpl с
// начала body. ok=false — в body не хватает байт на все поля шаблона (см.
// вызывающий код про трактовку паддинга vs повреждения).
func decodeOneRecord(tmpl Template, body []byte) (Record, int, bool) {
	fields := make(map[FieldKey][]byte, len(tmpl.Fields))
	off := 0

	for _, f := range tmpl.Fields {
		length := int(f.Length)

		if f.Length == variableLength {
			if len(body)-off < 1 {
				return Record{}, 0, false
			}
			l := int(body[off])
			off++
			if l == 255 {
				if len(body)-off < 2 {
					return Record{}, 0, false
				}
				l = int(binary.BigEndian.Uint16(body[off : off+2]))
				off += 2
			}
			length = l
		}

		if len(body)-off < length {
			return Record{}, 0, false
		}
		value := make([]byte, length)
		copy(value, body[off:off+length])
		fields[f.Key] = value
		off += length
	}

	return Record{TemplateID: tmpl.ID, Fields: fields}, off, true
}
