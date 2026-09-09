// Command loadgen sends a rate-limited stream of UDP packets at a target
// collector, for manual load testing and for producing the PPS numbers that
// belong in docs/benchmarks.md. It currently sends fixed-size filler
// payloads; protocol-accurate NetFlow/IPFIX/Syslog payloads are layered on
// top once internal/netflow, internal/ipfix and internal/syslog exist.
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
	size := flag.Int("size", 64, "payload size in bytes per packet")
	flag.Parse()

	if *pps <= 0 {
		return fmt.Errorf("pps must be positive, got %d", *pps)
	}
	if *size <= 0 {
		return fmt.Errorf("size must be positive, got %d", *size)
	}

	conn, err := net.Dial("udp", *target)
	if err != nil {
		return fmt.Errorf("dial %s: %w", *target, err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			slog.Warn("close udp connection", "err", cerr)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()

	payload := make([]byte, *size)
	ticker := time.NewTicker(time.Second / time.Duration(*pps))
	defer ticker.Stop()

	var sent, failed uint64
	for {
		select {
		case <-ctx.Done():
			slog.Info("loadgen finished", "sent", sent, "failed", failed, "target", *target)
			return nil
		case <-ticker.C:
			if _, err := conn.Write(payload); err != nil {
				failed++
				continue
			}
			sent++
		}
	}
}
