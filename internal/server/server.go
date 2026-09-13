// Package server provides the collector's operational HTTP surface:
// liveness/readiness and pprof. It is deliberately separate from any future
// public REST/gRPC API in internal/api, since that one serves consumers and
// this one serves operators.
package server

import (
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// New builds an *http.Server exposing /healthz, /debug/pprof/* and /metrics
// (scraped from reg) on addr. The caller owns the server's lifecycle
// (ListenAndServe + Shutdown).
func New(addr string, log *slog.Logger, reg prometheus.Gatherer) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler(log))
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func healthzHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			// The client already received the 200 status line; a failed body
			// write only matters for diagnosing a misbehaving health checker.
			log.Warn("healthz: write response", "err", err)
		}
	}
}
