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

_(empty — first entry lands with the NetFlow v5 parser, Roadmap.md stage 3)_

Template for each entry:

```
### <date> — <what changed>

Before: <benchstat output>
After:  <benchstat output>
Why:    <one line — what made the difference>
```
