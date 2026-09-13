// Command loadgen sends a rate-limited stream of UDP packets at a target
// collector, for manual load testing and for producing the PPS numbers that
// belong in docs/benchmarks.md. By default it sends fixed-size filler
// payloads for raw throughput testing; -netflow5 switches to
// protocol-accurate NetFlow v5 packets so the full
// ingest -> parse -> storage path can actually be exercised end-to-end.
// IPFIX/Syslog payloads will be layered on once those parsers exist.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SultanIsaev/umbrella/internal/netflow"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	target := flag.String("target", "127.0.0.1:2055", "UDP address of the collector under test")
	pps := flag.Int("pps", 1000, "packets per second to send")
	duration := flag.Duration("duration", 10*time.Second, "how long to send traffic")
	size := flag.Int("size", 64, "payload size in bytes per packet (ignored when -netflow5 is set)")
	sendNetflow5 := flag.Bool("netflow5", false, "send protocol-accurate NetFlow v5 packets instead of filler payload")
	records := flag.Int("records", 10, "flow records per packet when -netflow5 is set (1-30)")
	flag.Parse()

	if *pps <= 0 {
		return fmt.Errorf("pps must be positive, got %d", *pps)
	}
	if *sendNetflow5 {
		if *records <= 0 || *records > 30 {
			return fmt.Errorf("records must be in [1,30], got %d", *records)
		}
	} else if *size <= 0 {
		return fmt.Errorf("size must be positive, got %d", *size)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", *target)
	if err != nil {
		return fmt.Errorf("dial %s: %w", *target, err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			slog.Warn("close udp connection", "err", cerr)
		}
	}()

	fillerPayload := make([]byte, *size)
	ticker := time.NewTicker(time.Second / time.Duration(*pps))
	defer ticker.Stop()

	var sent, failed uint64
	var seq uint32
	for {
		select {
		case <-ctx.Done():
			slog.Info("loadgen finished", "sent", sent, "failed", failed, "target", *target)
			return nil
		case <-ticker.C:
			payload := fillerPayload
			if *sendNetflow5 {
				seq++
				p, err := netflowV5Packet(*records, seq)
				if err != nil {
					// records уже провалидирован выше в допустимый диапазон,
					// так что EncodeV5 тут не должен падать — если упал,
					// это баг в построении пакета, а не во входных данных.
					return fmt.Errorf("build netflow v5 packet: %w", err)
				}
				payload = p
			}

			if _, err := conn.Write(payload); err != nil {
				failed++
				continue
			}
			sent++
		}
	}
}

// netflowV5Packet собирает один синтетический, но валидный NetFlow v5 пакет
// с заданным числом flow-записей и растущим FlowSequence — как у реального
// экспортёра, чтобы через collector можно было проверить весь путь
// ingest -> parse -> storage, а не только доставку сырых байт по UDP.
func netflowV5Packet(recordCount int, seq uint32) ([]byte, error) {
	now := time.Now()
	header := netflow.Header{
		Version:      5,
		Count:        uint16(recordCount),
		UnixSecs:     uint32(now.Unix()),
		UnixNsecs:    uint32(now.Nanosecond()),
		FlowSequence: seq,
	}

	records := make([]netflow.Record, recordCount)
	for i := range records {
		records[i] = netflow.Record{
			SrcAddr: [4]byte{10, 0, 0, 1},
			DstAddr: [4]byte{10, 0, byte(i / 256), byte(i % 256)},
			SrcPort: uint16(1024 + i),
			DstPort: 80,
			Packets: 1,
			Bytes:   64,
			Prot:    6, // TCP
		}
	}

	return netflow.EncodeV5(header, records)
}
