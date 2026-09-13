package ipfix

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Хелперы сборки сырых IPFIX-сообщений для тестов. Публичного энкодера
// нет и не нужен: коллектор только принимает IPFIX от реальных экспортёров.

func uint16Bytes(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func uint32Bytes(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func setBytes(setID uint16, body []byte) []byte {
	length := setHeaderSize + len(body)
	b := make([]byte, length)
	binary.BigEndian.PutUint16(b[0:2], setID)
	binary.BigEndian.PutUint16(b[2:4], uint16(length))
	copy(b[4:], body)
	return b
}

func templateSetBytes(templates ...Template) []byte {
	var body []byte
	for _, tmpl := range templates {
		body = append(body, uint16Bytes(tmpl.ID)...)
		body = append(body, uint16Bytes(uint16(len(tmpl.Fields)))...)
		for _, f := range tmpl.Fields {
			rawID := f.Key.ElementID
			if f.Key.EnterpriseNumber != 0 {
				rawID |= enterpriseBit
			}
			body = append(body, uint16Bytes(rawID)...)
			body = append(body, uint16Bytes(f.Length)...)
			if f.Key.EnterpriseNumber != 0 {
				body = append(body, uint32Bytes(f.Key.EnterpriseNumber)...)
			}
		}
	}
	return setBytes(templateSetID, body)
}

func dataSetBytes(templateID uint16, records ...[]byte) []byte {
	var body []byte
	for _, r := range records {
		body = append(body, r...)
	}
	return setBytes(templateID, body)
}

// varLenValue кодирует v как переменной длины поле по RFC 7011 §7: один
// байт длины, если len(v) < 255, иначе 0xFF + 2-байтовая длина.
func varLenValue(v []byte) []byte {
	if len(v) < 255 {
		return append([]byte{byte(len(v))}, v...)
	}
	b := append([]byte{255}, uint16Bytes(uint16(len(v)))...)
	return append(b, v...)
}

func packet(domainID uint32, sets ...[]byte) []byte {
	var body []byte
	for _, s := range sets {
		body = append(body, s...)
	}
	total := headerSize + len(body)

	h := make([]byte, headerSize)
	binary.BigEndian.PutUint16(h[0:2], protocolVersion)
	binary.BigEndian.PutUint16(h[2:4], uint16(total))
	binary.BigEndian.PutUint32(h[8:12], 1) // sequence number, не проверяется в тестах
	binary.BigEndian.PutUint32(h[12:16], domainID)

	return append(h, body...)
}

func fixedFieldTemplate(id uint16, elementID uint16, length uint16) Template {
	return Template{ID: id, Fields: []FieldSpec{{Key: FieldKey{ElementID: elementID}, Length: length}}}
}

func TestDecode_TemplateThenData(t *testing.T) {
	cache := NewTemplateCache()

	tmplPkt := packet(100, templateSetBytes(fixedFieldTemplate(256, 8, 4))) // 8 = sourceIPv4Address
	_, records, err := cache.Decode("exporter-a", tmplPkt)
	require.NoError(t, err)
	require.Empty(t, records)
	require.Equal(t, uint64(0), cache.UnknownTemplate())

	dataPkt := packet(100, dataSetBytes(256, []byte{10, 0, 0, 1}, []byte{10, 0, 0, 2}))
	header, records, err := cache.Decode("exporter-a", dataPkt)
	require.NoError(t, err)
	require.Equal(t, uint32(100), header.ObservationDomainID)
	require.Len(t, records, 2)
	require.Equal(t, []byte{10, 0, 0, 1}, records[0].Fields[FieldKey{ElementID: 8}])
	require.Equal(t, []byte{10, 0, 0, 2}, records[1].Fields[FieldKey{ElementID: 8}])
}

// TestDecode_DataBeforeTemplate — тот же ключевой edge case, что и для v9
// (общий template-механизм): Data Set на неизвестный шаблон не роняет
// сообщение и не виснет, дропается с метрикой, восстанавливается после
// прихода шаблона.
func TestDecode_DataBeforeTemplate(t *testing.T) {
	cache := NewTemplateCache()

	dataPkt := packet(1, dataSetBytes(256, []byte{10, 0, 0, 1}))
	_, records, err := cache.Decode("exporter-a", dataPkt)
	require.NoError(t, err, "неизвестный шаблон не должен приводить к ошибке сообщения")
	require.Empty(t, records)
	require.Equal(t, uint64(1), cache.UnknownTemplate())

	tmplPkt := packet(1, templateSetBytes(fixedFieldTemplate(256, 8, 4)))
	_, _, err = cache.Decode("exporter-a", tmplPkt)
	require.NoError(t, err)

	dataPkt2 := packet(1, dataSetBytes(256, []byte{10, 0, 0, 2}))
	_, records, err = cache.Decode("exporter-a", dataPkt2)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, []byte{10, 0, 0, 2}, records[0].Fields[FieldKey{ElementID: 8}])
	require.Equal(t, uint64(1), cache.UnknownTemplate())
}

// TestDecode_VariableLengthField — новое по сравнению с v9: реальная длина
// значения не фиксирована шаблоном, а закодирована перед самим значением.
// Проверяет обе формы: короткую (<255 байт) и длинную (0xFF + uint16).
func TestDecode_VariableLengthField(t *testing.T) {
	cache := NewTemplateCache()

	tmpl := Template{ID: 256, Fields: []FieldSpec{{Key: FieldKey{ElementID: 82}, Length: variableLength}}} // 82 = interfaceName
	_, _, err := cache.Decode("exporter-a", packet(1, templateSetBytes(tmpl)))
	require.NoError(t, err)

	shortValue := []byte("eth0")
	longValue := make([]byte, 300)
	for i := range longValue {
		longValue[i] = byte('a' + i%26)
	}

	dataPkt := packet(1, dataSetBytes(256, varLenValue(shortValue), varLenValue(longValue)))
	_, records, err := cache.Decode("exporter-a", dataPkt)
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, shortValue, records[0].Fields[FieldKey{ElementID: 82}])
	require.Equal(t, longValue, records[1].Fields[FieldKey{ElementID: 82}])
}

// TestDecode_EnterpriseField проверяет, что vendor-specific IE (enterprise
// bit) не путается со стандартным IE с тем же числовым ElementID.
func TestDecode_EnterpriseField(t *testing.T) {
	cache := NewTemplateCache()

	standard := FieldSpec{Key: FieldKey{ElementID: 1}, Length: 4}
	enterprise := FieldSpec{Key: FieldKey{EnterpriseNumber: 9, ElementID: 1}, Length: 4} // тот же ElementID=1, другой вендор
	tmpl := Template{ID: 256, Fields: []FieldSpec{standard, enterprise}}

	_, _, err := cache.Decode("exporter-a", packet(1, templateSetBytes(tmpl)))
	require.NoError(t, err)

	dataPkt := packet(1, dataSetBytes(256, joinBytes([]byte{1, 1, 1, 1}, []byte{2, 2, 2, 2})))
	_, records, err := cache.Decode("exporter-a", dataPkt)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, []byte{1, 1, 1, 1}, records[0].Fields[FieldKey{ElementID: 1}])
	require.Equal(t, []byte{2, 2, 2, 2}, records[0].Fields[FieldKey{EnterpriseNumber: 9, ElementID: 1}])
}

func joinBytes(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

func TestDecode_DifferentExportersIsolateTemplates(t *testing.T) {
	cache := NewTemplateCache()

	_, _, err := cache.Decode("exporter-a", packet(1, templateSetBytes(fixedFieldTemplate(256, 8, 4))))
	require.NoError(t, err)

	tmplB := Template{ID: 256, Fields: []FieldSpec{{Key: FieldKey{ElementID: 7}, Length: 2}, {Key: FieldKey{ElementID: 11}, Length: 2}}}
	_, _, err = cache.Decode("exporter-b", packet(1, templateSetBytes(tmplB)))
	require.NoError(t, err)

	_, recordsA, err := cache.Decode("exporter-a", packet(1, dataSetBytes(256, []byte{9, 9, 9, 9})))
	require.NoError(t, err)
	require.Len(t, recordsA, 1)
	require.Equal(t, []byte{9, 9, 9, 9}, recordsA[0].Fields[FieldKey{ElementID: 8}])

	_, recordsB, err := cache.Decode("exporter-b", packet(1, dataSetBytes(256, []byte{0, 80, 1, 187})))
	require.NoError(t, err)
	require.Len(t, recordsB, 1)
	require.Equal(t, []byte{0, 80}, recordsB[0].Fields[FieldKey{ElementID: 7}])
	require.Equal(t, []byte{1, 187}, recordsB[0].Fields[FieldKey{ElementID: 11}])
}

func TestDecode_Malformed(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "сообщение короче заголовка",
			data: make([]byte, headerSize-1),
		},
		{
			name: "неподдерживаемая версия",
			data: func() []byte {
				p := packet(1)
				binary.BigEndian.PutUint16(p[0:2], 9)
				return p
			}(),
		},
		{
			name: "header.Length не совпадает с реальным размером сообщения",
			data: func() []byte {
				p := packet(1, templateSetBytes(fixedFieldTemplate(256, 8, 4)))
				binary.BigEndian.PutUint16(p[2:4], uint16(len(p)+10))
				return p
			}(),
		},
		{
			name: "set объявляет длину больше, чем осталось байт",
			data: func() []byte {
				set := templateSetBytes(fixedFieldTemplate(256, 8, 4))
				binary.BigEndian.PutUint16(set[2:4], uint16(len(set)+100))
				return packet(1, set)
			}(),
		},
		{
			name: "template declares больше полей, чем есть байт",
			data: func() []byte {
				set := templateSetBytes(fixedFieldTemplate(256, 8, 4))
				binary.BigEndian.PutUint16(set[6:8], 5) // fieldCount вместо 1
				return packet(1, set)
			}(),
		},
		{
			name: "enterprise bit установлен, но не хватает байт на enterprise number",
			data: func() []byte {
				set := templateSetBytes(fixedFieldTemplate(256, 8, 4))
				binary.BigEndian.PutUint16(set[8:10], 8|enterpriseBit) // помечаем поле enterprise, не добавляя 4 байта
				return packet(1, set)
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewTemplateCache()
			_, _, err := cache.Decode("exporter-a", tt.data)
			require.Error(t, err)
		})
	}
}

func TestDecode_MalformedDataSet(t *testing.T) {
	cache := NewTemplateCache()
	_, _, err := cache.Decode("exporter-a", packet(1, templateSetBytes(fixedFieldTemplate(256, 8, 4))))
	require.NoError(t, err)

	// Известный шаблон требует 4 байта на запись, а Data Set содержит
	// только 2 — не ошибка сообщения, дроп с метрикой.
	shortData := packet(1, dataSetBytes(256, []byte{1, 2}))
	_, records, err := cache.Decode("exporter-a", shortData)
	require.NoError(t, err)
	require.Empty(t, records)
	require.Equal(t, uint64(1), cache.MalformedDataSet())
}

// TestDecode_DataSetTrailingPadding — паддинг ПОСЛЕ хотя бы одной успешно
// разобранной записи не должен считаться malformed, в отличие от случая
// TestDecode_MalformedDataSet, где не удаётся разобрать ни одной записи.
func TestDecode_DataSetTrailingPadding(t *testing.T) {
	cache := NewTemplateCache()
	_, _, err := cache.Decode("exporter-a", packet(1, templateSetBytes(fixedFieldTemplate(256, 8, 4))))
	require.NoError(t, err)

	// Одна валидная запись (4 байта) + 2 байта паддинга.
	dataPkt := packet(1, dataSetBytes(256, []byte{10, 0, 0, 1}, []byte{0, 0}))
	_, records, err := cache.Decode("exporter-a", dataPkt)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, []byte{10, 0, 0, 1}, records[0].Fields[FieldKey{ElementID: 8}])
	require.Equal(t, uint64(0), cache.MalformedDataSet())
}

func TestDecode_ConcurrentAccess(t *testing.T) {
	cache := NewTemplateCache()
	const goroutines = 8
	const itersPerGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		templateID := uint16(256 + g%3)

		go func() {
			defer wg.Done()
			tmplPkt := packet(1, templateSetBytes(fixedFieldTemplate(templateID, 8, 4)))
			dataPkt := packet(1, dataSetBytes(templateID, []byte{1, 2, 3, 4}))

			for i := 0; i < itersPerGoroutine; i++ {
				if i%2 == 0 {
					_, _, err := cache.Decode("exporter-a", tmplPkt)
					require.NoError(t, err)
				} else {
					_, _, err := cache.Decode("exporter-a", dataPkt)
					require.NoError(t, err)
				}
			}
		}()
	}
	wg.Wait()
}
