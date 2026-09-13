package netflow

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Хелперы сборки сырых NetFlow v9 пакетов для тестов. ---
//
// Публичного энкодера v9 нет и не нужен: коллектор только принимает трафик
// от реальных экспортёров, никогда не отправляет NetFlow v9 сам. Эти
// хелперы — тестовая утварь, не production API.

func v9Header(count uint16, sourceID uint32) []byte {
	b := make([]byte, headerV9Size)
	binary.BigEndian.PutUint16(b[0:2], protocolVersionV9)
	binary.BigEndian.PutUint16(b[2:4], count)
	binary.BigEndian.PutUint32(b[16:20], sourceID)
	return b
}

func v9FlowSet(flowSetID uint16, body []byte) []byte {
	length := flowSetHeaderSize + len(body)
	b := make([]byte, length)
	binary.BigEndian.PutUint16(b[0:2], flowSetID)
	binary.BigEndian.PutUint16(b[2:4], uint16(length))
	copy(b[4:], body)
	return b
}

func v9TemplateFlowSet(templates ...TemplateV9) []byte {
	var body []byte
	for _, tmpl := range templates {
		rec := make([]byte, 4+len(tmpl.Fields)*4)
		binary.BigEndian.PutUint16(rec[0:2], tmpl.ID)
		binary.BigEndian.PutUint16(rec[2:4], uint16(len(tmpl.Fields)))
		for i, f := range tmpl.Fields {
			off := 4 + i*4
			binary.BigEndian.PutUint16(rec[off:off+2], f.Type)
			binary.BigEndian.PutUint16(rec[off+2:off+4], f.Length)
		}
		body = append(body, rec...)
	}
	return v9FlowSet(templateFlowSetID, body)
}

func v9DataFlowSet(templateID uint16, records ...[]byte) []byte {
	var body []byte
	for _, r := range records {
		body = append(body, r...)
	}
	return v9FlowSet(templateID, body)
}

func joinBytes(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

// srcAddrTemplate — минимальный шаблон из одного 4-байтового поля (тип 8 —
// IPV4_SRC_ADDR по стандартному словарю NetFlow v9/IPFIX), достаточный для
// всех тестов ниже.
func srcAddrTemplate(id uint16) TemplateV9 {
	return TemplateV9{ID: id, Fields: []FieldSpec{{Type: 8, Length: 4}}}
}

func TestDecodeV9_TemplateThenData(t *testing.T) {
	cache := NewTemplateCache()

	templatePkt := joinBytes(
		v9Header(1, 100),
		v9TemplateFlowSet(srcAddrTemplate(256)),
	)
	_, records, err := cache.DecodeV9("exporter-a", templatePkt)
	require.NoError(t, err)
	require.Empty(t, records)
	require.Equal(t, uint64(0), cache.UnknownTemplate())

	dataPkt := joinBytes(
		v9Header(1, 100),
		v9DataFlowSet(256, []byte{10, 0, 0, 1}, []byte{10, 0, 0, 2}),
	)
	header, records, err := cache.DecodeV9("exporter-a", dataPkt)
	require.NoError(t, err)
	require.Equal(t, uint32(100), header.SourceID)
	require.Len(t, records, 2)
	require.Equal(t, []byte{10, 0, 0, 1}, records[0].Fields[8])
	require.Equal(t, []byte{10, 0, 0, 2}, records[1].Fields[8])
	require.Equal(t, uint64(0), cache.UnknownTemplate())
}

// TestDecodeV9_DataBeforeTemplate — ключевой edge case DoD M5: Data FlowSet
// на ещё не известный шаблон не должен ронять пакет и не должен виснуть —
// он дропается с метрикой, остальной пакет разбирается как обычно, а после
// того как шаблон всё же придёт (в следующем пакете), декодирование
// восстанавливается.
func TestDecodeV9_DataBeforeTemplate(t *testing.T) {
	cache := NewTemplateCache()

	dataPkt := joinBytes(
		v9Header(1, 100),
		v9DataFlowSet(256, []byte{10, 0, 0, 1}),
	)
	header, records, err := cache.DecodeV9("exporter-a", dataPkt)
	require.NoError(t, err, "неизвестный шаблон не должен приводить к ошибке пакета")
	require.Empty(t, records)
	require.Equal(t, uint32(100), header.SourceID)
	require.Equal(t, uint64(1), cache.UnknownTemplate())

	// Шаблон приходит с опозданием.
	templatePkt := joinBytes(
		v9Header(1, 100),
		v9TemplateFlowSet(srcAddrTemplate(256)),
	)
	_, _, err = cache.DecodeV9("exporter-a", templatePkt)
	require.NoError(t, err)

	// Новый Data FlowSet с тем же Template ID теперь декодируется —
	// старые, уже дропнутые данные из первого пакета не восстанавливаются
	// (кэш не буферизует сырые данные — см. DecodeV9 doc), это осознанное
	// решение, а не баг.
	dataPkt2 := joinBytes(
		v9Header(1, 100),
		v9DataFlowSet(256, []byte{10, 0, 0, 2}),
	)
	_, records, err = cache.DecodeV9("exporter-a", dataPkt2)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, []byte{10, 0, 0, 2}, records[0].Fields[8])
	require.Equal(t, uint64(1), cache.UnknownTemplate(), "счётчик не должен расти на успешно декодированном FlowSet")
}

func TestDecodeV9_MultipleTemplatesInOneFlowSet(t *testing.T) {
	cache := NewTemplateCache()

	pkt := joinBytes(
		v9Header(1, 1),
		v9TemplateFlowSet(srcAddrTemplate(256), srcAddrTemplate(257)),
	)
	_, _, err := cache.DecodeV9("exporter-a", pkt)
	require.NoError(t, err)

	dataPkt := joinBytes(
		v9Header(2, 1),
		v9DataFlowSet(256, []byte{1, 1, 1, 1}),
		v9DataFlowSet(257, []byte{2, 2, 2, 2}),
	)
	_, records, err := cache.DecodeV9("exporter-a", dataPkt)
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, uint16(256), records[0].TemplateID)
	require.Equal(t, uint16(257), records[1].TemplateID)
}

func TestDecodeV9_DifferentExportersIsolateTemplates(t *testing.T) {
	cache := NewTemplateCache()

	// exporter-a: шаблон 256 — одно поле по 4 байта.
	pktA := joinBytes(v9Header(1, 1), v9TemplateFlowSet(srcAddrTemplate(256)))
	_, _, err := cache.DecodeV9("exporter-a", pktA)
	require.NoError(t, err)

	// exporter-b: тот же Template ID 256, но другая раскладка — два поля по 2 байта.
	tmplB := TemplateV9{ID: 256, Fields: []FieldSpec{{Type: 7, Length: 2}, {Type: 11, Length: 2}}}
	pktB := joinBytes(v9Header(1, 1), v9TemplateFlowSet(tmplB))
	_, _, err = cache.DecodeV9("exporter-b", pktB)
	require.NoError(t, err)

	dataA := joinBytes(v9Header(1, 1), v9DataFlowSet(256, []byte{9, 9, 9, 9}))
	_, recordsA, err := cache.DecodeV9("exporter-a", dataA)
	require.NoError(t, err)
	require.Len(t, recordsA, 1)
	require.Equal(t, []byte{9, 9, 9, 9}, recordsA[0].Fields[8])

	dataB := joinBytes(v9Header(1, 1), v9DataFlowSet(256, []byte{0, 80, 1, 187}))
	_, recordsB, err := cache.DecodeV9("exporter-b", dataB)
	require.NoError(t, err)
	require.Len(t, recordsB, 1)
	require.Equal(t, []byte{0, 80}, recordsB[0].Fields[7])
	require.Equal(t, []byte{1, 187}, recordsB[0].Fields[11])
}

func TestDecodeV9_Malformed(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "пакет короче заголовка",
			data: make([]byte, headerV9Size-1),
		},
		{
			name: "неподдерживаемая версия",
			data: func() []byte {
				h := v9Header(0, 1)
				binary.BigEndian.PutUint16(h[0:2], 5)
				return h
			}(),
		},
		{
			name: "flowset объявляет длину больше, чем осталось байт",
			data: func() []byte {
				fs := v9TemplateFlowSet(srcAddrTemplate(256))
				binary.BigEndian.PutUint16(fs[2:4], uint16(len(fs)+100))
				return joinBytes(v9Header(1, 1), fs)
			}(),
		},
		{
			name: "flowset короче собственного заголовка",
			data: func() []byte {
				fs := v9TemplateFlowSet(srcAddrTemplate(256))
				binary.BigEndian.PutUint16(fs[2:4], 2)
				return joinBytes(v9Header(1, 1), fs)
			}(),
		},
		{
			name: "template declares больше полей, чем есть байт",
			data: func() []byte {
				fs := v9TemplateFlowSet(srcAddrTemplate(256))
				binary.BigEndian.PutUint16(fs[6:8], 5) // fieldCount вместо 1
				return joinBytes(v9Header(1, 1), fs)
			}(),
		},
		{
			name: "header.Count не совпадает с реальным числом flowset'ов",
			data: joinBytes(v9Header(2, 1), v9TemplateFlowSet(srcAddrTemplate(256))),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewTemplateCache()
			_, _, err := cache.DecodeV9("exporter-a", tt.data)
			require.Error(t, err)
		})
	}
}

func TestDecodeV9_MalformedDataFlowSet(t *testing.T) {
	cache := NewTemplateCache()
	pkt := joinBytes(v9Header(1, 1), v9TemplateFlowSet(srcAddrTemplate(256)))
	_, _, err := cache.DecodeV9("exporter-a", pkt)
	require.NoError(t, err)

	// Data FlowSet на известный шаблон (нужно 4 байта на запись), но
	// содержит только 2 байта — не ошибка пакета, дроп с метрикой.
	shortData := joinBytes(v9Header(1, 1), v9DataFlowSet(256, []byte{1, 2}))
	_, records, err := cache.DecodeV9("exporter-a", shortData)
	require.NoError(t, err)
	require.Empty(t, records)
	require.Equal(t, uint64(1), cache.MalformedDataFlowSet())
}

func TestDecodeV9_ConcurrentAccess(t *testing.T) {
	cache := NewTemplateCache()
	const goroutines = 8
	const itersPerGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		exporter := "exporter-a"
		templateID := uint16(256 + g%3) // несколько горутин делят один и тот же templateID

		go func() {
			defer wg.Done()
			tmplPkt := joinBytes(v9Header(1, 1), v9TemplateFlowSet(srcAddrTemplate(templateID)))
			dataPkt := joinBytes(v9Header(1, 1), v9DataFlowSet(templateID, []byte{1, 2, 3, 4}))

			for i := 0; i < itersPerGoroutine; i++ {
				if i%2 == 0 {
					_, _, err := cache.DecodeV9(exporter, tmplPkt)
					require.NoError(t, err)
				} else {
					_, _, err := cache.DecodeV9(exporter, dataPkt)
					require.NoError(t, err)
				}
			}
		}()
	}
	wg.Wait()
}
