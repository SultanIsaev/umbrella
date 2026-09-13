# Benchmarks

Every entry here is a `benchstat` comparison for one concrete optimization —
not a one-off number. Reproduce with:

```sh
go test -run=^$ -bench=<Name> -benchmem -count=10 ./... > old.txt
# apply the change
go test -run=^$ -bench=<Name> -benchmem -count=10 ./... > new.txt
benchstat old.txt new.txt
```

For end-to-end throughput (PPS/EPS against a running collector), use
`cmd/loadgen`:

```sh
go run ./cmd/loadgen -target 127.0.0.1:2055 -pps 50000 -duration 30s
```

## Log

### 2026-09-10 — NetFlow v5 EncodeV5/DecodeV5: manual byte packing instead of encoding/binary reflection

Before: `binary.Write(w, order, header)` / `binary.Read(r, order, &record)` —
each struct argument goes through reflection (`encoding/binary`'s `encoder`/`decoder`),
which allocates a fresh scratch buffer per call.

After: `binary.BigEndian.PutUint16/32` / `.Uint16/32` writing directly into one pre-sized
`[]byte` (`putHeader`/`putRecord`/`getHeader`/`getRecord` in
`internal/netflow/{encoderv5,decoderv5}.go`), so the whole packet is built or
read with exactly one allocation regardless of record count.

```
                      │   old.txt    │              new.txt                │
                      │    sec/op    │    sec/op     vs base               │
EncodeV5-8              1153.00n ± 3%   45.10n ±  1%  -96.09% (p=0.000 n=10)
EncodeV5_MaxRecords-8   22024.0n ± 5%   498.2n ± 25%  -97.74% (p=0.000 n=10)
DecodeV5-8                635.2n ± 5%   54.72n ±  3%  -91.39% (p=0.000 n=10)
DecodeV5_MaxRecords-8   11866.5n ± 7%   698.3n ±  2%  -94.12% (p=0.000 n=10)
geomean                    3.720µ        171.2n        -95.40%

                      │   old.txt    │              new.txt                │
                      │  allocs/op   │ allocs/op   vs base                 │
EncodeV5-8                4.000 ± 0%   1.000 ± 0%  -75.00% (p=0.000 n=10)
EncodeV5_MaxRecords-8    33.000 ± 0%   1.000 ± 0%  -96.97% (p=0.000 n=10)
DecodeV5-8                4.000 ± 0%   1.000 ± 0%  -75.00% (p=0.000 n=10)
DecodeV5_MaxRecords-8    33.000 ± 0%   1.000 ± 0%  -96.97% (p=0.000 n=10)
geomean                   11.49        1.000       -91.30%
```

Why: `binary.Write`/`binary.Read` allocate one scratch buffer per struct
argument via reflection — for a 30-record packet that's 1 (header) + 30 (records) = 31 extra allocations on top of the
output buffer, all on the hot
ingest path. Packing bytes by hand with `encoding/binary.BigEndian` needs no
reflection and writes straight into the already-sized output slice, so
allocation count drops to 1 regardless of record count (1 to 30).

### 2026-09-13 — pipeline.Result.Events: hand-rolled IPv4 formatting instead of net.IP.String()

Found via `pprof` (CPU profile captured under real `loadgen -netflow5` load
against a running collector, not a guess): `net.IP.String()` was a visible
chunk of CPU time on the hot path, formatting `[4]byte` fields that are
always IPv4 (`netflow.Record.SrcAddr`/`DstAddr`), never IPv6.

Before: `net.IP(rec.SrcAddr[:]).String()` — generic IPv4/IPv6 detection and
formatting for an address whose family is already known.

After: `formatIPv4`/`appendDecimalByte` (`internal/pipeline/pool.go`) write
decimal digits directly into a stack-allocated `[15]byte`, one `string()`
conversion at the end.

```
                          │ events_old.txt │           events_new.txt           │
                          │     sec/op     │   sec/op     vs base                │
ResultEvents-8                   395.8n ± 1%   376.9n ± 2%  -4.78% (p=0.001 n=10)
ResultEvents_MaxRecords-8         11.27µ ± 4%   10.45µ ± 1%  -7.32% (p=0.000 n=10)
geomean                           2.112µ        1.984µ       -6.06%
```

Why: skips `net.IP.String()`'s generic dual-family logic — CPU-time win only.
Allocs/op and B/op are unchanged (432 B, 8 allocs — confirmed via benchstat,
`~ (p=1.000)`): the same single `string()` conversion is unavoidable either
way. The dominant allocation cost on this path is actually the
`map[string]any` construction in the same function (~4.4GB of ~6GB
allocated in a profiled load run) — that needs `storage.Event.Fields`'s
type to change to fix, a separate, larger, cross-backend piece of work not
done here.

### 2026-09-13 — storage.Event.Fields: ordered []Field instead of map[string]any

Follow-up to the entry above — this is the actual fix for the allocation
cost that one didn't touch. Confirmed via an isolated micro-benchmark before
touching production code (constructing a 7-entry `map[string]any` vs
`[]struct{Key string; Value any}`, forced to escape via a package-level
sink so the compiler couldn't optimize the allocation away): map construction
was 2 allocs/op vs 1 alloc/op for the slice.

Before: `storage.Event.Fields map[string]any` — `internal/pipeline.Result.Events`
builds one as a map literal per NetFlow record; every non-string value
(`uint16`/`uint8`/`uint32`) is boxed into `any` separately, plus the map's
own bucket allocation.

After: `storage.Fields []Field` (`Field{Key string; Value any}`,
`internal/storage/storage.go`) — one allocation for the whole backing
array. `Fields.MarshalJSON`/`UnmarshalJSON` keep the exact wire shape
`map[string]any` had (a flat JSON object) so ClickHouse's `JSONExtract` and
any Kafka consumer see no difference — verified with a round-trip test
(`internal/storage/storage_test.go`).

```
                          │ events_new.txt │       events_fields_new.txt        │
                          │     sec/op      │    sec/op     vs base              │
ResultEvents-8                    376.9n ± 2%   237.0n ±  5%  -37.12% (p=0.000 n=10)
ResultEvents_MaxRecords-8        10.449µ ± 1%   7.746µ ± 17%  -25.87% (p=0.002 n=10)
geomean                           1.984µ        1.355µ        -31.73%

                          │ events_new.txt │       events_fields_new.txt        │
                          │      B/op       │     B/op      vs base              │
ResultEvents-8                    432.0 ± 0%     336.0 ± 0%  -22.22% (p=0.000 n=10)
ResultEvents_MaxRecords-8       12.719Ki ± 0%   9.938Ki ± 0%  -21.87% (p=0.000 n=10)
geomean                          2.316Ki        1.806Ki       -22.04%

                          │ events_new.txt │       events_fields_new.txt       │
                          │   allocs/op     │ allocs/op   vs base                │
ResultEvents-8                    8.000 ± 0%   7.000 ± 0%  -12.50% (p=0.000 n=10)
ResultEvents_MaxRecords-8         211.0 ± 0%   181.0 ± 0%  -14.22% (p=0.000 n=10)
geomean                           41.09        35.59       -13.36%
```

Why: a slice literal needs one allocation for its backing array; a map
literal needs a hashmap header plus buckets, on top of boxing the same
values into `any` either way — the map's own structure is pure overhead a
slice doesn't have. `Fields` stays generic across protocols (no
NetFlow-specific struct baked into `storage.Event` — IPFIX/Syslog/CEF will
build their own `[]Field` with different keys later), unlike hard-coding a
typed struct, which would have been faster still but broken that
protocol-agnostic contract.

Template for each entry:

```
### <date> — <what changed>

Before: <benchstat output>
After:  <benchstat output>
Why:    <one line — what made the difference>
```
