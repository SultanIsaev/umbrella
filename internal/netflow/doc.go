// Package netflow decodes Cisco NetFlow v5 and v9 wire formats: fixed-length
// binary records for v5, and template-based records for v9 (which requires
// caching each exporter's template definitions before its data records can
// be decoded).
//
// V5 encode/decode (EncodeV5, DecodeV5) and V9 decode (TemplateCache.DecodeV9)
// are implemented and wired into pipeline.Pool. V9 fields are exposed as raw
// bytes keyed by their IE type (see pipeline.NetflowV9Result.Events) —
// semantic interpretation of individual Information Elements is left for
// later, tracked in Roadmap.md.
package netflow
