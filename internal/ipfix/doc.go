// Package ipfix decodes IPFIX (RFC 7011), NetFlow v9's template-based
// successor, sharing the exporter/template caching strategy with
// internal/netflow's v9 decoder. Two differences from v9 drove the extra
// complexity here: Field Specifiers can carry a vendor Enterprise Number
// (see FieldKey), and Field Length can be variableLength, meaning the
// actual per-record length is encoded inline before the value (RFC 7011 §7)
// instead of being fixed by the template alone.
package ipfix
