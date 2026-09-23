package netflow

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
)

// templateKey идентифицирует шаблон в разрезе конкретного экспортёра и его
// observation domain (SourceID) — один и тот же Template ID у разных
// экспортёров (или разных observation domain одного устройства) может
// означать совершенно разные раскладки полей.
type templateKey struct {
	exporter   string
	sourceID   uint32
	templateID uint16
}

// TemplateCache хранит шаблоны NetFlow v9, полученные из Template FlowSet'ов,
// и использует их для разбора последующих Data FlowSet'ов.
//
// В отличие от v5, где DecodeV5 — чистая функция без состояния, для v9 кэш
// шаблонов обязателен: экспортёр присылает Template FlowSet не в каждом
// пакете, а Data FlowSet без известного шаблона в принципе нельзя разобрать
// на отдельные записи (известна только суммарная длина FlowSet'а).
//
// Безопасен для конкурентного использования (RWMutex: чтение шаблона на
// разбор Data FlowSet — частый путь, запись нового шаблона — редкий).
type TemplateCache struct {
	mu        sync.RWMutex
	templates map[templateKey]TemplateV9

	unknownTemplate      atomic.Uint64
	malformedDataFlowSet atomic.Uint64
}

// NewTemplateCache создаёт пустой кэш шаблонов.
func NewTemplateCache() *TemplateCache {
	return &TemplateCache{templates: make(map[templateKey]TemplateV9)}
}

// UnknownTemplate — сколько Data FlowSet'ов пришлось дропнуть, потому что
// соответствующий шаблон ещё не известен ("data record раньше template").
// Это ожидаемый кейс при потере/переупорядочивании UDP-пакетов или при
// первом пакете от нового экспортёра, а не ошибка разбора — поэтому
// метрика, а не error.
func (c *TemplateCache) UnknownTemplate() uint64 {
	return c.unknownTemplate.Load()
}

// MalformedDataFlowSet — сколько Data FlowSet'ов имели длину меньше размера
// одной записи по уже известному шаблону (шаблон есть, но данные ему не
// соответствуют).
func (c *TemplateCache) MalformedDataFlowSet() uint64 {
	return c.malformedDataFlowSet.Load()
}

// DecodeV9 разбирает пакет NetFlow v9: обновляет кэш шаблонов из Template
// FlowSet'ов и декодирует Data FlowSet'ы, для которых шаблон уже известен.
//
// exporterKey идентифицирует источник пакета (в проде — адрес отправителя
// UDP, см. ingest.Packet.From, которым pipeline.Pool.decode заполняет этот
// аргумент). Вызывающий код передаёт его явно, а не читает из глобального
// состояния, что делает кэш тестируемым независимо от остального пайплайна.
//
// Ошибка возвращается только при нарушении структуры пакета (не хватает
// байт на объявленный FlowSet, версия не 9, Count не совпадает с реально
// пройденным числом FlowSet'ов) — когда дальше разобрать пакет невозможно.
// Data FlowSet с неизвестным шаблоном — НЕ ошибка (см. UnknownTemplate):
// пакет разбирается дальше, такой FlowSet просто пропускается по его
// собственной объявленной длине.
func (c *TemplateCache) DecodeV9(exporterKey string, data []byte) (HeaderV9, []RecordV9, error) {
	if len(data) < headerV9Size {
		return HeaderV9{}, nil, fmt.Errorf("netflow v9: packet too short: %d bytes, need at least %d for header", len(data), headerV9Size)
	}

	header := getHeaderV9(data[:headerV9Size])
	if header.Version != protocolVersionV9 {
		return HeaderV9{}, nil, fmt.Errorf("netflow v9: unsupported version %d, expected %d", header.Version, protocolVersionV9)
	}

	remaining := data[headerV9Size:]
	var records []RecordV9
	var flowSetCount uint16

	for len(remaining) >= flowSetHeaderSize {
		flowSetID := binary.BigEndian.Uint16(remaining[0:2])
		length := binary.BigEndian.Uint16(remaining[2:4])

		if int(length) < flowSetHeaderSize {
			return HeaderV9{}, nil, fmt.Errorf("netflow v9: flowset length %d shorter than flowset header (%d)", length, flowSetHeaderSize)
		}
		if int(length) > len(remaining) {
			return HeaderV9{}, nil, fmt.Errorf("netflow v9: flowset declares %d bytes, only %d remain in packet", length, len(remaining))
		}

		body := remaining[flowSetHeaderSize:length]
		remaining = remaining[length:]
		flowSetCount++

		switch {
		case flowSetID == templateFlowSetID:
			if err := c.learnTemplates(exporterKey, header.SourceID, body); err != nil {
				return HeaderV9{}, nil, err
			}
		case flowSetID == optionsTemplateFlowSet:
			// Options Template — вне рамок этого шага, безопасно
			// пропускаем по объявленной длине (уже сделано выше).
		case flowSetID >= minDataFlowSetID:
			if recs, ok := c.decodeDataFlowSet(exporterKey, header.SourceID, flowSetID, body); ok {
				records = append(records, recs...)
			}
		default:
			// FlowSet ID 2-255 зарезервированы спецификацией и не должны
			// встречаться на практике — не роняем весь пакет, пропускаем.
		}
	}
	// Меньше 4 байт, оставшихся в remaining — паддинг до конца пакета,
	// не ошибка.

	if header.Count != flowSetCount {
		return HeaderV9{}, nil, fmt.Errorf("netflow v9: header.Count=%d does not match %d flowset(s) actually present", header.Count, flowSetCount)
	}

	return header, records, nil
}

// learnTemplates разбирает Template FlowSet: он может содержать несколько
// Template Record'ов подряд, вплоть до паддинга в конце FlowSet'а.
func (c *TemplateCache) learnTemplates(exporterKey string, sourceID uint32, body []byte) error {
	for len(body) >= 4 {
		templateID := binary.BigEndian.Uint16(body[0:2])
		fieldCount := binary.BigEndian.Uint16(body[2:4])
		body = body[4:]

		need := int(fieldCount) * 4
		if len(body) < need {
			return fmt.Errorf("netflow v9: template %d declares %d field(s), only %d byte(s) remain", templateID, fieldCount, len(body))
		}

		fields := make([]FieldSpec, fieldCount)
		for i := range fields {
			off := i * 4
			fields[i] = FieldSpec{
				Type:   binary.BigEndian.Uint16(body[off : off+2]),
				Length: binary.BigEndian.Uint16(body[off+2 : off+4]),
			}
		}
		body = body[need:]

		key := templateKey{exporter: exporterKey, sourceID: sourceID, templateID: templateID}
		c.mu.Lock()
		c.templates[key] = TemplateV9{ID: templateID, Fields: fields}
		c.mu.Unlock()
	}
	// Меньше 4 байт, оставшихся в body — паддинг до конца FlowSet'а,
	// не ошибка.
	return nil
}

// decodeDataFlowSet разбирает Data FlowSet на отдельные записи по уже
// известному шаблону. Возвращает ok=false, если шаблон неизвестен или
// данные ему не соответствуют — в обоих случаях FlowSet дропается с
// увеличением соответствующей метрики, а не ошибкой всего пакета.
func (c *TemplateCache) decodeDataFlowSet(exporterKey string, sourceID uint32, templateID uint16, body []byte) ([]RecordV9, bool) {
	key := templateKey{exporter: exporterKey, sourceID: sourceID, templateID: templateID}

	c.mu.RLock()
	tmpl, ok := c.templates[key]
	c.mu.RUnlock()

	if !ok {
		c.unknownTemplate.Add(1)
		return nil, false
	}

	recSize := tmpl.recordSize()
	if recSize == 0 || len(body) < recSize {
		c.malformedDataFlowSet.Add(1)
		return nil, false
	}

	count := len(body) / recSize
	records := make([]RecordV9, 0, count)
	for i := 0; i < count; i++ {
		rec := body[i*recSize : (i+1)*recSize]
		fields := make(map[uint16][]byte, len(tmpl.Fields))
		off := 0
		for _, f := range tmpl.Fields {
			// Копируем, а не берём подсрез rec: RecordV9 не должен
			// незаметно ссылаться на буфер вызывающего кода (тот же
			// принцип, что и в DecodeV5 — там это достигается копированием
			// в [N]byte-поля, здесь поля переменной длины, поэтому нужна
			// явная копия).
			value := make([]byte, f.Length)
			copy(value, rec[off:off+int(f.Length)])
			fields[f.Type] = value
			off += int(f.Length)
		}
		records = append(records, RecordV9{TemplateID: templateID, Fields: fields})
	}
	return records, true
}
