# testdata

Fixture files consumed by table-driven tests and fuzz corpora:

- `netflow/` — captured or hand-built NetFlow v5/v9 packets (valid and
  deliberately malformed).
- `ipfix/` — IPFIX packets, including template-definition records.
- `syslog/` — RFC 5424 messages, including edge cases (partial TCP frames,
  structured data).
- `cef/` — CEF messages.
- `pcap/` — raw `.pcap` captures used by the optional `gopacket`/`AF_PACKET`
  exploration (Roadmap.md, stage 3).

Keep fixtures small and check them into git — they are the seed corpus for
`go test -fuzz` as well as the unit tests.
