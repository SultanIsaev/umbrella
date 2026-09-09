// Package netflow decodes Cisco NetFlow v5 and v9 wire formats: fixed-length
// binary records for v5, and template-based records for v9 (which requires
// caching each exporter's template definitions before its data records can
// be decoded).
//
// Not yet implemented — tracked in Roadmap.md, stage 3.
package netflow
