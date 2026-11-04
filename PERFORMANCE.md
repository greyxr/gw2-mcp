# Performance verification

The serial implementation was captured before this optimization pass. Both workloads now exceed the requested 2× improvement in the reproducible API-latency benchmark.

| Workload | Serial median | Parallel median | Speedup | Requests per operation |
|---|---:|---:|---:|---:|
| Account overview | 61.14 ms | 20.54 ms | 2.98× | 3 before and after |
| Multi-item inventory across 10 characters | 285.47 ms | 102.59 ms | 2.78× | 14 before and after |

Each benchmark uses the same HTTP transport fixture with 20 ms latency per request, three measured samples and five operations per sample. It checks authentication, expected account data, item totals, locations and source coverage. The improvement comes from three independent overview reads running concurrently and inventory reads using at most four workers. Private account responses are not cached or omitted. Results retain deterministic ordering and partial-source errors.

Run the current benchmarks:

```sh
GOCACHE=/tmp/gw2-go-build go test ./internal/gw2 -run '^$' -bench '^(BenchmarkAccountOverview|BenchmarkFindItemsComplex)$' -count=3 -benchtime=5x -benchmem
```

Raw measurements and fixture details are in [benchmarks/serial.txt](benchmarks/serial.txt) and [benchmarks/parallel.txt](benchmarks/parallel.txt). The serial source snapshots remain in `/tmp/gw2-performance-baseline` for this session; they are not duplicated into the maintained codebase.

The structural refactor retained the performance behavior: median overview **20.53 ms**, complex inventory **101.48 ms**, with the same 20 ms simulated request latency, expected results, and 3/14 requests per operation. See [benchmarks/refactor.txt](benchmarks/refactor.txt). The simplified fixture now prebuilds its fixed response bodies, so allocation counts should not be used to claim application memory improvements against the older fixture.

## Live network check

The serial source snapshots were also built into a separate executable and compared through the same MCP protocol, using real account reads and alternating execution order after warmup.

| Completed live pair | Serial | Parallel |
|---|---:|---:|
| Overview, first pair | 0.5250 s | 0.1995 s |
| Overview, second pair | 0.5932 s | 1.1299 s |
| Inventory, first pair | 38.2937 s | 11.1888 s |

The later serial inventory read returned incomplete source coverage, so the live run was stopped rather than treating partial data as a faster equivalent result. These live samples show network variability and do not establish a production latency guarantee. The reproducible benchmark results above are the basis of the 2× claim.

## Five-second stretch check

A fresh MCP process completed the three-source evidence check for the T4 Fractals question in **1.926 seconds**: account achievement progress, the relevant achievement category, and its achievement definitions. This measures tool/backend evidence retrieval, not model generation time or a universal latency guarantee.

The check found no historical completion timestamp or attributable loot history. A quick unsupported answer remains unsupported: the workflow must report missing evidence rather than manufacture a completion date or material earnings. No Fractal IDs, dates, or reward values were hardcoded into the application to obtain this timing.

`quote_trades` also reduces tool round trips for broader planning: up to 50 independent quotes share one metadata lookup and one depth lookup instead of issuing separate requests for every scenario. Shared books do not reserve liquidity or make repeated rows cumulative.

Validation included `go test -race ./...`, `go vet ./...`, and executable MCP smoke checks against live public and authenticated endpoints.
