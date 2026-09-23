package pipeline

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/SultanIsaev/umbrella/internal/ingest"
	"github.com/SultanIsaev/umbrella/internal/ipfix"
	"github.com/SultanIsaev/umbrella/internal/netflow"
	"github.com/SultanIsaev/umbrella/internal/storage"
)

// versionSize — все три поддерживаемых формата (NetFlow v5/v9, IPFIX) несут
// версию протокола в первых 2 байтах пакета (Big-Endian uint16) — этого
// достаточно, чтобы выбрать декодер, не разбирая пакет целиком.
const versionSize = 2

const (
	netflowV5Version = 5
	netflowV9Version = 9
	ipfixVersion     = 10
)

// Result — единица телеметрии, разобранная воркером Pool: обёртка над одним
// декодированным пакетом одного из поддерживаемых форматов, которая знает,
// как превратить себя в нормализованные storage.Event. Новый формат
// добавляется новой реализацией этого интерфейса и веткой в Pool.decode —
// без изменений в Pool.Run/cmd/collector.
type Result interface {
	Events() []storage.Event
}

// NetflowV5Result — результат разбора NetFlow v5.
type NetflowV5Result struct {
	Header  netflow.Header
	Records []netflow.Record
}

// Events превращает один разобранный NetFlow v5 пакет в набор нормализованных
// событий — по одному на каждый Record (это и есть единица телеметрии, а не пакет целиком).
//
// Timestamp — приближение через Header.UnixSecs (время экспорта пакета),
// не точное время потока. Точный расчёт требует пересчёта через дельты
// SysUptime/First/Last — оставлено на потом, не блокирует M4.
func (r NetflowV5Result) Events() []storage.Event {
	events := make([]storage.Event, len(r.Records))
	for i, rec := range r.Records {
		events[i] = storage.Event{
			Timestamp: int64(r.Header.UnixSecs),
			Source:    "netflow5",
			Fields: storage.Fields{
				{Key: "src_addr", Value: formatIPv4(rec.SrcAddr)},
				{Key: "dst_addr", Value: formatIPv4(rec.DstAddr)},
				{Key: "src_port", Value: rec.SrcPort},
				{Key: "dst_port", Value: rec.DstPort},
				{Key: "protocol", Value: rec.Prot},
				{Key: "packets", Value: rec.Packets},
				{Key: "bytes", Value: rec.Bytes},
			},
		}
	}
	return events
}

// NetflowV9Result — результат разбора NetFlow v9.
type NetflowV9Result struct {
	Header  netflow.HeaderV9
	Records []netflow.RecordV9
}

// Events превращает один разобранный NetFlow v9 пакет в набор нормализованных
// событий.
//
// В отличие от v5, поля здесь — сырые байты по типу (RecordV9.Fields
// map[uint16][]byte): семантическая интерпретация конкретных Information
// Element (адрес, порт, счётчик — их 100+ стандартных в NetFlow v9)
// сознательно оставлена на потом (см. netflow.RecordV9 doc) — не блокирует
// M5, но означает, что Fields здесь — "field_<type>": hex(value)", а не
// человекочитаемые имена, как в v5.
//
// Ключи сортируются перед сборкой Fields — map[uint16][]byte в Go не имеет
// детерминированного порядка обхода, а недетерминированный порядок полей
// сделал бы тесты и сравнение событий флаки.
func (r NetflowV9Result) Events() []storage.Event {
	events := make([]storage.Event, len(r.Records))
	for i, rec := range r.Records {
		events[i] = storage.Event{
			Timestamp: int64(r.Header.UnixSecs),
			Source:    "netflow9",
			Fields:    rawFieldsByType(rec.Fields),
		}
	}
	return events
}

// rawFieldsByType строит storage.Fields из map[uint16][]byte в
// детерминированном порядке (по возрастанию типа поля).
func rawFieldsByType(fields map[uint16][]byte) storage.Fields {
	types := make([]uint16, 0, len(fields))
	for t := range fields {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })

	out := make(storage.Fields, len(types))
	for i, t := range types {
		out[i] = storage.Field{Key: fmt.Sprintf("field_%d", t), Value: hex.EncodeToString(fields[t])}
	}
	return out
}

// IPFIXResult — результат разбора IPFIX.
type IPFIXResult struct {
	Header  ipfix.Header
	Records []ipfix.Record
}

// Events превращает один разобранный IPFIX-пакет в набор нормализованных
// событий — те же оговорки про сырые поля, что и у NetflowV9Result.Events
// (см. ipfix.Record doc), плюс Enterprise Number в имени поля для
// vendor-specific Information Element.
func (r IPFIXResult) Events() []storage.Event {
	events := make([]storage.Event, len(r.Records))
	for i, rec := range r.Records {
		events[i] = storage.Event{
			Timestamp: int64(r.Header.ExportTime),
			Source:    "ipfix",
			Fields:    rawFieldsByKey(rec.Fields),
		}
	}
	return events
}

// rawFieldsByKey строит storage.Fields из map[ipfix.FieldKey][]byte в
// детерминированном порядке (по возрастанию EnterpriseNumber, затем ElementID).
func rawFieldsByKey(fields map[ipfix.FieldKey][]byte) storage.Fields {
	keys := make([]ipfix.FieldKey, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].EnterpriseNumber != keys[j].EnterpriseNumber {
			return keys[i].EnterpriseNumber < keys[j].EnterpriseNumber
		}
		return keys[i].ElementID < keys[j].ElementID
	})

	out := make(storage.Fields, len(keys))
	for i, k := range keys {
		name := fmt.Sprintf("ie_%d", k.ElementID)
		if k.EnterpriseNumber != 0 {
			name = fmt.Sprintf("ie_%d.%d", k.EnterpriseNumber, k.ElementID)
		}
		out[i] = storage.Field{Key: name, Value: hex.EncodeToString(fields[k])}
	}
	return out
}

// formatIPv4 форматирует 4 байта в точечно-десятичную нотацию
// ("10.0.0.1") без net.IP.String(): тот делает общую IPv4/IPv6
// детекцию и форматирование под оба случая, чего здесь не нужно — тип
// адреса уже точно известен ([4]byte из NetFlow v5 Record). Профилирование
// под loadgen (docs/benchmarks.md) показало net.IP.String() заметной
// частью аллокаций на этом горячем пути.
func formatIPv4(ip [4]byte) string {
	var buf [15]byte // "255.255.255.255" — максимум 15 символов
	n := 0
	for i, b := range ip {
		if i > 0 {
			buf[n] = '.'
			n++
		}
		n += appendDecimalByte(buf[n:], b)
	}
	return string(buf[:n])
}

// appendDecimalByte пишет десятичное представление b в dst (без ведущих
// нулей) и возвращает число записанных байт. dst должен вмещать минимум 3
// байта — вызывающий код (formatIPv4) это гарантирует.
func appendDecimalByte(dst []byte, b byte) int {
	switch {
	case b >= 100:
		dst[0] = '0' + b/100
		dst[1] = '0' + b/10%10
		dst[2] = '0' + b%10
		return 3
	case b >= 10:
		dst[0] = '0' + b/10
		dst[1] = '0' + b%10
		return 2
	default:
		dst[0] = '0' + b
		return 1
	}
}

type Pool struct {
	workers int
	invalid atomic.Uint64

	// v9Cache/ipfixCache — общие на все воркеры кэши шаблонов: оба типа уже
	// безопасны для конкурентного использования (RWMutex, см. их doc), а
	// шаблоны одного экспортёра должны быть видны всем воркерам одинаково,
	// а не каждому свой изолированный кэш.
	v9Cache    *netflow.TemplateCache
	ipfixCache *ipfix.TemplateCache
}

func New(workers int) (*Pool, error) {
	if workers <= 0 {
		return nil, fmt.Errorf("invalid count of workers: %d", workers)
	}
	return &Pool{
		workers:    workers,
		v9Cache:    netflow.NewTemplateCache(),
		ipfixCache: ipfix.NewTemplateCache(),
	}, nil
}

func (p *Pool) Run(in <-chan ingest.Packet, results chan<- Result) {
	var wg sync.WaitGroup
	wg.Add(p.workers)
	for range p.workers {
		go func() {
			defer wg.Done()
			for pkt := range in {
				result, err := p.decode(pkt)
				if err != nil {
					p.invalid.Add(1)
					continue
				}
				results <- result
			}
		}()
	}
	wg.Wait()
	close(results) // Pool - оркестратор для results, закрывает только он и только тут
}

// decode определяет формат пакета по версии протокола (первые 2 байта,
// общие для NetFlow v5/v9 и IPFIX) и зовёт соответствующий декодер.
// exporterKey (адрес отправителя) нужен только v9/IPFIX — они стейтфулны
// (кэш шаблонов в разрезе экспортёра), v5 его игнорирует.
func (p *Pool) decode(pkt ingest.Packet) (Result, error) {
	if len(pkt.Data) < versionSize {
		return nil, fmt.Errorf("pipeline: packet too short to contain a version: %d bytes", len(pkt.Data))
	}

	exporterKey := pkt.From.String()
	switch binary.BigEndian.Uint16(pkt.Data[:versionSize]) {
	case netflowV5Version:
		header, records, err := netflow.DecodeV5(pkt.Data)
		if err != nil {
			return nil, err
		}
		return NetflowV5Result{Header: header, Records: records}, nil

	case netflowV9Version:
		header, records, err := p.v9Cache.DecodeV9(exporterKey, pkt.Data)
		if err != nil {
			return nil, err
		}
		return NetflowV9Result{Header: header, Records: records}, nil

	case ipfixVersion:
		header, records, err := p.ipfixCache.Decode(exporterKey, pkt.Data)
		if err != nil {
			return nil, err
		}
		return IPFIXResult{Header: header, Records: records}, nil

	default:
		return nil, fmt.Errorf("pipeline: unsupported protocol version %d", binary.BigEndian.Uint16(pkt.Data[:versionSize]))
	}
}

func (p *Pool) Invalid() uint64 {
	return p.invalid.Load()
}
