// Package storage задаёт сторону-приёмник пайплайна: контракт, в который
// internal/pipeline пишет нормализованные события, независимо от того,
// какой backend (internal/storage/clickhouse, internal/storage/kafka или
// фейк для тестов) его реализует.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Field — одна пара ключ-значение внутри Fields.
type Field struct {
	Key   string
	Value any
}

// Fields — упорядоченный набор полей события. Слайс вместо map[string]any
// осознанно: профилирование под реальной нагрузкой показало,
// что построение map[string]any (internal/pipeline.Result.Events) было главным источником
// аллокаций — свежая хеш-таблица плюс аллокация на упаковку (boxing)
// каждого значения в any. Слайсу структур нужна одна аллокация на весь
// массив, без хеширования и без отдельных накладных расходов на упаковку
// каждого значения.
//
// MarshalJSON/UnmarshalJSON сохраняют ту же форму на проводе, что была у
// map[string]any — плоский JSON-объект, а не массив
// {"Key":...,"Value":...} — поэтому ClickHouse (JSONExtract) и любой
// консьюмер в Kafka не видят разницы.
type Fields []Field

// MarshalJSON записывает f как плоский JSON-объект, например
// {"src_addr":"10.0.0.1"}.
func (f Fields) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, field := range f {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(field.Key)
		if err != nil {
			return nil, fmt.Errorf("storage: marshal field key %q: %w", field.Key, err)
		}
		buf.Write(key)
		buf.WriteByte(':')

		value, err := json.Marshal(field.Value)
		if err != nil {
			return nil, fmt.Errorf("storage: marshal field %q value: %w", field.Key, err)
		}
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON разбирает плоский JSON-объект обратно в Fields. Порядок
// полей не сохраняется (JSON-объект неупорядочен — encoding/json разбирает
// его через Go-мапу), а числовые значения возвращаются как float64 — это
// стандартное поведение encoding/json для значений, разбираемых в any, а
// не особенность именно Fields.
func (f *Fields) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	fields := make(Fields, 0, len(raw))
	for key, rawValue := range raw {
		var value any
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return fmt.Errorf("storage: unmarshal field %q: %w", key, err)
		}
		fields = append(fields, Field{Key: key, Value: value})
	}
	*f = fields
	return nil
}

// Event — нормализованная запись телеметрии, полученная от парсера
// (internal/netflow, internal/ipfix, internal/syslog, internal/cef) и,
// опционально, internal/enrich.
type Event struct {
	Timestamp int64
	Source    string
	Fields    Fields
}

// Storage сохраняет батчи Event. Реализации обязаны быть безопасны для
// конкурентного использования несколькими воркерами pipeline.
type Storage interface {
	Write(ctx context.Context, events []Event) error
	Close() error
}
