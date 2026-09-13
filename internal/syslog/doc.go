// Package syslog decodes RFC 5424 syslog messages, including structured
// data elements. Decode itself is transport-agnostic and ready for UDP
// (one datagram per message, no reassembly needed); TCP framing (messages
// arrive as an unbounded byte stream, RFC 6587 octet-counting or
// newline-delimited) is not yet implemented.
package syslog
