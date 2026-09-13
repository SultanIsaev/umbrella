package observability

import "github.com/prometheus/client_golang/prometheus"

// Metrics collects the collector's Prometheus metrics under one registry,
// exposed via /metrics (see internal/server).
//
// Counters/gauges that just mirror state another package already tracks
// (ingest.Listener.Received, pipeline.Pool.Invalid, channel length, ...)
// are wired via RegisterCounterFunc/RegisterGaugeFunc, sampled fresh on
// every scrape — not duplicated with a separate increment call. This keeps
// internal/ingest, internal/pipeline etc. free of any Prometheus
// dependency; only the orchestrator (cmd/collector) knows metrics exist.
// StorageWriteDuration is the exception: nothing else tracks per-call
// write latency, so it's a real Histogram the caller Observes into
// directly.
type Metrics struct {
	Registry *prometheus.Registry

	StorageEventsWritten prometheus.Counter
	StorageWriteErrors   prometheus.Counter
	StorageWriteDuration prometheus.Histogram
}

// NewMetrics creates a fresh registry with the metrics this package owns
// outright (see the type doc-comment for why gauges/counters mirroring
// other packages' state are registered separately via
// RegisterCounterFunc/RegisterGaugeFunc, not here).
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,
		StorageEventsWritten: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "umbrella",
			Subsystem: "storage",
			Name:      "events_written_total",
			Help:      "Total number of events successfully written to the storage backend.",
		}),
		StorageWriteErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "umbrella",
			Subsystem: "storage",
			Name:      "write_errors_total",
			Help:      "Total number of storage.Write calls that returned an error.",
		}),
		StorageWriteDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "umbrella",
			Subsystem: "storage",
			Name:      "write_duration_seconds",
			Help:      "Latency of storage.Write calls (one call per parsed packet's worth of events).",
			Buckets:   prometheus.DefBuckets,
		}),
	}

	reg.MustRegister(m.StorageEventsWritten, m.StorageWriteErrors, m.StorageWriteDuration)
	return m
}

// RegisterCounterFunc exposes an existing monotonic counter (e.g.
// ingest.Listener.Received) as a Prometheus counter under
// umbrella_<subsystem>_<name>, sampled fresh on every scrape. f must be
// safe to call concurrently (promhttp may scrape while other goroutines
// are still running) and must only ever increase — both already true for
// the atomic.Uint64-backed getters this wraps.
func (m *Metrics) RegisterCounterFunc(subsystem, name, help string, f func() float64) {
	m.Registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: "umbrella",
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	}, f))
}

// RegisterGaugeFunc exposes a live value (e.g. current channel length via
// len(ch)) as a Prometheus gauge under umbrella_<subsystem>_<name>, sampled
// fresh on every scrape.
func (m *Metrics) RegisterGaugeFunc(subsystem, name, help string, f func() float64) {
	m.Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "umbrella",
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	}, f))
}
