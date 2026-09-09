// Package syslog decodes RFC 5424 syslog messages, including structured
// data elements, and handles the framing differences between UDP (one
// datagram per message) and TCP (messages must be delimited explicitly).
//
// Not yet implemented — tracked in Roadmap.md, stage 3.
package syslog
