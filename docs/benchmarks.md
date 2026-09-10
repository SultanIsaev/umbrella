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

Template for each entry:

```
### <date> — <what changed>

Before: <benchstat output>
After:  <benchstat output>
Why:    <one line — what made the difference>
```
