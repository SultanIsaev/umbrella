package ingest

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
)

// Для NetFlow нормально будет и 1500 (<= 1464 байта),
// но более крупную датаграмму он будет обрезать.
// Но ingest задуман как общий приемник для будущих протоколов,
// то максимальный теоретический payload
//
// максимум payload UDP/IPv4 (65535 − 20 IP − 8 UDP = 65507);
// буфер такого размера гарантирует, что ReadFromUDP
// не обрежет молча ни одну валидную UDP-датаграмму
const bufferSize = 65507

// socketRecvBuffer — целевой SO_RCVBUF (буфер ядра под очередь ещё не
// прочитанных датаграмм), не путать с bufferSize (размер одного read).
// Он должен пережить кратковременный бёрст — например, синхронный флаш
// NetFlow-кэша у множества экспортёров с одинаковым active-timeout,
// interrupt coalescing на NIC, или паузу нашего read-цикла из-за GC/
// scheduler jitter — но этот буфер не спасает от устойчивой перегрузки
// (для этого есть drop+метрика в dispatch).
//
// Расчёт: target_PPS × avg_packet_size × burst_window × overhead_margin
//
//	50 000 pps × 1400 байт (NetFlow-экспортёр держится под Ethernet MTU 1500) × 50 мс (типичный запас на GC/scheduler-паузу) ≈ 3.5 МБ чистой потребности;
//	×2 запас на служебные накладные расходы ядра на пакет
//	в приёмном буфере (без запаса буфер размером ровно с датаграмму не вмещает даже её саму) → ~7 МБ, округлено до 8 МБ.
//
// ВАЖНО: SetReadBuffer тихо обрезает запрошенное значение до потолка ОС
// (net.core.rmem_max на Linux, kern.ipc.maxsockbuf на Darwin) — без ошибки.
// Это значение бесполезно без соответствующей настройки sysctl на хосте при деплое.
const socketRecvBuffer = 8 << 20 // 8 МБ

type Listener struct {
	conn      *net.UDPConn
	received  atomic.Uint64
	dropped   atomic.Uint64 // Метрика дропов для backpressure
	truncated atomic.Uint64 // Метрика усеченных пакетов
}

// Packet — сырые данные одной UDP-датаграммы вместе с адресом отправителя.
// Адрес нужен не самому ingest, а pipeline.Pool: NetFlow v9/IPFIX стейтфулны
// (шаблоны кэшируются в разрезе экспортёра — см.
// netflow.TemplateCache.DecodeV9/ipfix.TemplateCache.Decode), NetFlow v5 его
// игнорирует, так как у него нет шаблонов.
type Packet struct {
	Data []byte
	From netip.AddrPort
}

func NewListener(addr string) (*Listener, error) {
	resolvedAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	udpConn, err := net.ListenUDP("udp", resolvedAddr)
	if err != nil {
		return nil, err
	}

	if err := udpConn.SetReadBuffer(socketRecvBuffer); err != nil {
		_ = udpConn.Close() // возвращаем основную ошибку, ошибку закрытия глушу осознанно
		return nil, fmt.Errorf("set socket recv buffer: %w", err)
	}

	return &Listener{
		conn: udpConn,
	}, nil
}

// Run запускает чтение UDP пакетов.
// out - параметр, а не поле Listener, и Run никогда его не закрывает:
// в будущем несколько Listener (SO_REUSEPORT) будут писать в один общий канал для масштабирования приёма
// и закрыть его вправе только оркестратор, дождавшийся остановки всех писателей,
// а не отдельный Listener.
func (l *Listener) Run(ctx context.Context, out chan<- Packet) error {
	stopContextWatch := context.AfterFunc(ctx, func() {
		_ = l.conn.Close()
	})
	defer stopContextWatch()
	defer l.conn.Close()

	buf := make([]byte, bufferSize)
	for {
		// ReadFromUDPAddrPort вместо ReadFromUDP: возвращает netip.AddrPort
		// без аллокации (в отличие от *net.UDPAddr), а адрес отправителя нам
		// теперь реально нужен дальше по пайплайну (см. Packet).
		n, addrPort, err := l.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			// Проверяем было ли закрытие сокета запланированным
			if ctx.Err() != nil {
				return nil // Завершаемся без ошибки
			}
			return fmt.Errorf("udp listener read error: %w", err)
		}

		if n == len(buf) {
			//	Стандартный net.UDPConn.ReadFromUDP не даёт доступа к флагу MSG_TRUNC
			// 	(это есть только в низкоуровневых golang.org/x/net/ipv4/ipv6 — overkill для сейчас).
			//  Единственный доступный признак "возможно обрезано" — n == len(buf).
			//  Раз я выставил буфер заведомо больше любого валидного пакета,
			//  такое совпадение стало бы очень подозрительным
			l.truncated.Add(1)
			continue // не шлем заведомо поврежденные данные дальше
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		l.dispatch(out, Packet{Data: data, From: addrPort})
	}
}

// dispatch отправляет пакет в out без блокировки: если у канала нет
// свободной ёмкости принять его немедленно, пакет дропается и учитывается
// в метрике. Вынесен из Run отдельным методом, чтобы backpressure-решение
// можно было тестировать детерминированно (через ёмкость буфера канала),
// не гоняясь за таймингом реального читателя.
func (l *Listener) dispatch(out chan<- Packet, pkt Packet) {
	l.received.Add(1)
	select {
	case out <- pkt:
		// Пакет успешно ушел в очередь на обработку.
	default:
		// Очередь переполнена (Drop + метрика)
		l.dropped.Add(1)
	}
}

// Dropped возвращает количество сброшенных пакетов (для экспорта метрик)
func (l *Listener) Dropped() uint64 {
	return l.dropped.Load()
}

// Received возвращает количество успешно прочитанных пакетов
func (l *Listener) Received() uint64 {
	return l.received.Load()
}

// Truncated возвращает количество урезанных пакетов
func (l *Listener) Truncated() uint64 {
	return l.truncated.Load()
}

func (l *Listener) Close() error {
	return l.conn.Close()
}

func (l *Listener) LocalAddr() net.Addr {
	return l.conn.LocalAddr()
}
