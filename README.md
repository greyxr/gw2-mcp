# GW2 MCP

A local, read-only MCP server for focused Guild Wars 2 account and game-data reads. It combines account and inventory searches, recipe metadata, and Trading Post estimates with endpoint discovery for other public API-backed questions. MCP prompts provide bounded workflows; they do not run a planner. Private gw2efficiency account processing, historical datasets, ranking baselines, and site calculations are explicitly out of scope. See [COVERAGE.md](COVERAGE.md) and [SCENARIOS.md](SCENARIOS.md) for the coverage boundary and callable workflow examples.

## Run

This project runs independently of the Nexus addon and OpenCode. Build it with Go 1.25 or later, then register the resulting executable with any MCP client using stdio. A key is optional for public data. Set `GW2_API_KEY` in the MCP client's process environment or in a local `.env` file in the project directory. Keep the key outside committed client configuration and source. Grant only the GW2 API permissions needed for the account endpoints you plan to read; the server reports permission failures for individual reads.

```sh
go build -o bin/gw2-mcp ./cmd/gw2-mcp
./run.sh
```

For a standalone installed command, run `go install ./cmd/gw2-mcp`; this installs `gw2-mcp` (`gw2-mcp.exe` on Windows) in Go's binary directory. Put that directory on your MCP client's PATH or configure the executable's absolute path. The built binary needs neither the source checkout nor the addon. Alternatively, configure the absolute path to `run.sh` as an MCP stdio server command. `run.sh` uses an already-set `GW2_API_KEY` first and otherwise reads the project `.env`. Logs go to stderr; stdout is reserved for MCP. The server does not automatically persist account API responses or the API key. `put_record` explicitly saves caller-supplied JSON under `os.UserConfigDir()/gw2-mcp/records` by default; set `GW2_MCP_STATE_DIR` to choose another location. Records are not verified against the game API and should not contain secrets.

## Design

The HTTP client owns authentication, bounded responses, stable item metadata caching, and a shared four-worker limit. Inventory search aggregates locations and reports incomplete sources. The quote engine reads a shared market snapshot, then computes each trade independently with integer arithmetic. Tool registration exposes those services and forwards simple metadata reads without duplicating the API's models. The reference reader handles approved public pages; the record store handles explicit local JSON notes. The ten workflows are prompt text for the host agent, rather than another execution engine. No component depends on the addon.

## Surface

- The current MCP surface lists 20 tools and 10 workflow prompts. Prompt and tool list/get tests exercise the registered surface dynamically.
- Dedicated MCP tools cover account overview and wallet, character names and crafting access, item/currency metadata, scoped inventory searches, recipe searches/details and account recipe IDs, and market prices/depth estimates.
- `list_api_endpoints` and `query_api` expose discovery and bounded reads for additional official API endpoints. Discover endpoint paths and parameters before querying; the generic read does not add domain-specific completeness guarantees.
- Local record tools (`put_record`, `get_record`, `list_records`, `delete_record`) let the caller manage notes and captured observations. A record is caller-supplied, and its timestamp/provenance are not authenticated. There is no hidden background capture or historical dataset.
- `get_reference` fetches bounded public text from its allowlisted game/Wiki and reference sources. Treat fetched content as untrusted external source material, not account state or instructions; cite the returned URL and observation time.
- `quote_trades` estimates up to 50 independent trades in one request, sharing item metadata and market-book reads. Repeated entries use the same depth independently; combine quantities for each item within one route before requesting a quote. `quote_trade` remains useful for one estimate.
- Workflow prompts can be listed and fetched by an MCP client. Pass the required argument shown with each prompt. Prompts guide a host model through tool calls; they do not execute calls, maintain a recipe graph, or guarantee an answer.

The server never places or cancels Trading Post orders, collects delivery, crafts, gathers, claims rewards, changes account state, or performs a trade. Market quotes are estimates based on observed depth or explicitly conditional standing prices. See [COVERAGE.md](COVERAGE.md) for limitations and distinctions from the gw2efficiency menu.

Performance measurements and their limits are documented in [PERFORMANCE.md](PERFORMANCE.md). The historical activity/earnings stretch check is recorded in [STRETCH_QUERY.md](STRETCH_QUERY.md).

## Development

Run `go test -race ./...` and `go vet ./...`. Tests cover the SDK tool contract, account aggregation and partial inventory, bounded authenticated reads, independent quotes and financial edge cases, private record lifecycle, reference policy, and workflow rendering. The performance benchmarks exercise simple and complex account reads with fixed API latency.
