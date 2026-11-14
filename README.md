# GW2 MCP

A local, read-only MCP server for Guild Wars 2 account and game data. It combines account and inventory searches, recipes, Trading Post estimates, and wiki lookups with bounded access to every other official API endpoint. MCP prompts provide reusable workflows; they are text for the host agent, not a planner.

## Run

This project runs independently of the Nexus addon and OpenCode. Build it with Go 1.25 or later, then register the resulting executable with any MCP client using stdio. A key is optional for public data. Set `GW2_API_KEY` in the MCP client's process environment or in a local `.env` file in the project directory. Keep the key outside committed client configuration and source. Grant only the GW2 API permissions needed for the account endpoints you plan to read; the server reports permission failures for individual reads.

```sh
go build -o bin/gw2-mcp ./cmd/gw2-mcp
./run.sh
```

For a standalone installed command, run `go install ./cmd/gw2-mcp`; this installs `gw2-mcp` (`gw2-mcp.exe` on Windows) in Go's binary directory. Put that directory on your MCP client's PATH or configure the executable's absolute path. The built binary needs neither the source checkout nor the addon. Alternatively, configure the absolute path to `run.sh` as an MCP stdio server command. `run.sh` uses an already-set `GW2_API_KEY` first and otherwise reads the project `.env`. Logs go to stderr; stdout is reserved for MCP. The server does not automatically persist account API responses or the API key. `put_record` explicitly saves caller-supplied JSON under `os.UserConfigDir()/gw2-mcp/records` by default; set `GW2_MCP_STATE_DIR` to choose another location. Records are not verified against the game API and should not contain secrets.

## Design

The HTTP client owns authentication, bounded responses, one retry of transient failures, stable item and currency metadata caching, and a shared four-worker limit. Batch reads tolerate unknown IDs: the API's partial (HTTP 206) and all-invalid (HTTP 404) answers become `missing`/`no_listings` lists instead of errors. Inventory search aggregates locations and reports incomplete sources. The quote engine reads one market snapshot, then computes each trade independently with integer arithmetic. The wiki reader searches and reads only the official Guild Wars 2 wiki; the record store handles explicit local JSON notes. The ten workflows are prompt text for the host agent, rather than another execution engine. No component depends on the addon.

Responses are built for small context cost: each result is one compact JSON (or plain text) block with no structured-content duplicate or output schema, generic API reads can be trimmed with `fields`, arrays are cut near 24,000 characters with a `next_offset`, single objects over 96,000 characters are refused, and wiki pages return only the article body as text.

## Surface

The MCP surface lists 19 tools and 10 workflow prompts.

- Account: `account_overview` (account details, key permissions, wallet with currency names), `list_characters`, `get_character_crafting`, `find_items` (bank, material storage, shared inventory, character bags).
- Game data: `search_wiki` (name to page title, type, and API IDs), `get_wiki_page` (page text or wikitext, treated as untrusted source material), `get_items`, `get_currencies`, `search_recipes` (full recipes with item names), `get_recipes`, `get_account_recipes` (which of the given recipes are unlocked).
- Trading Post: `get_market_prices` and `quote_trades` (up to 50 depth-aware estimates with seller fees). Rows are independent; combine quantities of an item into one row.
- Generic API: `list_api_endpoints` (optionally by prefix) and `query_api` for every other enabled v2 route, with `ids` (including `all` on small endpoints), paging, `lang`, route filters, `fields`, and `offset`.
- Local records: `put_record`, `get_record`, `list_records`, `delete_record`. Records are caller-supplied and unauthenticated; there is no background capture.
- Workflow prompts guide a host model through tool calls; they do not execute calls or guarantee an answer.

The server never places or cancels Trading Post orders, collects delivery, crafts, gathers, claims rewards, changes account state, or performs a trade. Market quotes are estimates based on observed depth or explicitly conditional standing prices; fees are 5% listing and 10% exchange with a 1 copper minimum, and exact in-game rounding may differ.

## Development

Run `go test -race ./...` and `go vet ./...`. Tests cover the SDK tool contract, account aggregation and partial inventory, bounded authenticated reads, independent quotes and financial edge cases, private record lifecycle, wiki reads, and workflow rendering.
