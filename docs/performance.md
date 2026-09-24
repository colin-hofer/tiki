# Performance baseline

Measured on 2026-09-24 with Go 1.27.1, Linux/amd64, an AMD Ryzen 9 7950X, and `GOMAXPROCS=32`. These are local store benchmarks, not HTTP request latencies or production capacity claims. Both runs used the same machine, benchmark source, and dependencies.

The temporary database was on **tmpfs**, with WAL and `synchronous=FULL`. Write timings therefore do **not** measure durable SSD commits. Use `TMPDIR` on the deployment filesystem when evaluating storage performance.

Read benchmarks seed 10,000 and 100,000 items in one untimed transaction, with short descriptions, a common tag on every item, and a rare tag and assignee on every hundredth item. One third of items have status `todo`. Lists request 50 items and use the real store queries and JSON membership decoding. Setup and password hashing are excluded from read timing.

## Results

Medians of three 200-ms samples at 100,000 items, before and after the Go foundation overhaul:

| Operation | Before, µs/op | After, µs/op | Before, B/op | After, B/op |
| --- | ---: | ---: | ---: | ---: |
| Get | 35.4 | 35.9 | 3,503 | 2,691 |
| List | 242.7 | 234.5 | 63,578 | 48,070 |
| Status filter | 254.2 | 257.4 | 63,708 | 48,169 |
| Rare tag | 2,626.9 | 1,690.9 | 68,327 | 52,692 |
| Two tags + status | 4,922.9 | 3,132.6 | 68,828 | 53,143 |
| Assignee | 1,123.6 | 1,089.8 | 67,953 | 52,412 |
| Parallel list | 244.4 | 109.5 | 63,098 | 50,806 |
| Create + move* | 371.6 | 307.2 | 20,796 | 14,135 |

Parallel list measures aggregate wall time divided by completed operations using `RunParallel`; it is a throughput metric, not individual request latency. The bounded read pool yields about 2.23× throughput here. Tag-filter times fall about 36%, and ordinary list allocation bytes fall about 24%. Lookup and status-filter timings are roughly unchanged; these short samples do not establish statistical significance for small differences.

*Create + move uses the existing growing-dataset benchmark, not the 100,000-item read fixture. Each iteration creates an assigned/tagged item and moves it before an anchor, occasionally rebalancing. Its size and rebalance frequency depend on iteration count, so treat it as a local regression signal. Removing full item hydration for the move's version/anchor checks reduces allocations from a median 490 to 389 per iteration.

Raw samples: [before](benchmarks/before.txt), [after](benchmarks/after.txt).

## Reproduce and investigate

```sh
go test ./internal/tiki -run '^$' -bench 'Benchmark(Read|CreateAndMove)' -benchmem -benchtime=200ms -count=3
make bench
```

For a CPU profile, select one workload rather than combining setup and unrelated operations:

```sh
go test ./internal/tiki -run '^$' -bench 'BenchmarkRead/100000/TagsAndStatus$' -benchtime=5s -cpuprofile=/tmp/tiki.cpu
go tool pprof /tmp/tiki.cpu
```

The read pool is capped at eight connections and never falls below two. Writes remain serialized on a separate connection; read snapshots cannot block ordinary WAL commits. Filters use indexed membership probes, and tag IDs are resolved once per query rather than repeatedly joining tag names for each candidate item. Lists omit descriptions and preallocate their bounded page buffer. No cache, custom JSON parser, or additional dependency was added.

Before claiming the acceptance targets in `PLAN.md`, measure the full HTTP workload on the target SSD and CPU allocation, with realistic large descriptions, dense and sparse memberships, mixed reads/writes, p50/p95/p99 latency, CPU, and RSS. Search, comments, and live subscribers are not implemented yet, so their targets remain untested. Whole-workspace priority rebalancing is still occasional O(n) maintenance; retain its correctness tests and measure its write pauses before replacing it with a more complicated algorithm.
