> Historical notes. The current server surface and setup are documented in [README.md](README.md); current scenario scope is in [COVERAGE.md](COVERAGE.md). Earlier tool counts and environment restrictions below describe previous sessions.

# GW2 Workflow API Research

## Scope and status

This research targets the existing read-only MCP server and the two user workflows:

- Compare crafting and selling Aurene's Fang to fund Aurene's Weight against crafting Aurene's Weight directly, using stored materials first and comparing instant transactions with standing listings/orders.
- Identify the untradeable currencies required for a recipe.

The server now exposes 12 composable tools, including recipe search/details, currency metadata, account wallet data, multi-item inventory search, character crafting disciplines, market quotes, and unlocked account recipes. These let an agent inspect recipe stages and combine live market/account observations without a server-side crafting planner.

**Documentation constraint:** direct Guild Wars Wiki page access returned HTTP 403 on 2026-11-02, although web search returned indexed content. Earlier endpoint research below relied on the official live API route catalog and existing implementation/live API observations. Indexed recipe evidence is not a verified complete acquisition graph. Field-level contracts and permissions that require unavailable documentation remain open.

The current target and acceptance rules are in `CRAFTING_WORKFLOW.md`. The MCP prompt `aurene_fang_or_weight` guides a host agent through the comparison using the existing twelve tools; it does not implement a server-side crafting planner. The principal gaps are a complete verified acquisition graph, including Mystic Forge/vendor stages, and live account/market verification in a network-enabled runtime. Adding unrelated account endpoints alone does not close those gaps.

## Evidence-backed endpoint inventory

The official [GW2 API v2 route catalog](https://api.guildwars2.com/v2.json) lists these routes as active. Its `auth` flag indicates whether the route requires API authentication; it does not specify the exact token scopes or response schemas.

| Endpoint | Catalog auth | Workflow relevance | Recommendation |
| --- | --- | --- | --- |
| `/v2/characters/:id/crafting` | Required | Character discipline/rating/active data; now exposed by `get_character_crafting`. | Implemented; live MCP call succeeded. |
| `/v2/characters/:id/recipes` | Required | Candidate source for character-specific recipe access. The distinction from `/v2/account/recipes` needs field-level confirmation. | Research after Wiki recovery; do not add an alias yet. |
| `/v2/guild/upgrades` | Public | Candidate resolver for `GuildUpgrade` recipe ingredients. | Strong low-cost candidate if target recipe chains contain guild-upgrade ingredients; verify schema first. |
| `/v2/commerce/delivery` | Required | Potential account view of uncollected Trading Post proceeds/items, which current `find_item` explicitly excludes. | Candidate for a direct `get_trading_post_delivery` tool if the returned data helps avoid double-counting expected funds/items. Verify schema and scopes first. |
| `/v2/commerce/transactions` | Required | Potential view of active Trading Post buy/sell orders, which current quotes do not inspect. | Candidate only if open orders materially affect the comparison; exact subroutes and semantics need documentation. |
| `/v2/account/legendaryarmory` | Required | Account-level legendary ownership/entitlement information. Could explain why a legendary is owned but not present in searched inventory. | Lower priority; it does not establish that an individual item instance is tradeable. Verify response semantics first. |
| `/v2/account/materials` and `/v2/account/wallet` | Required | Material storage and currency balances. | Already used by `find_items` and `account_overview`; metadata comes from `get_currencies`. |
| `/v2/commerce/prices` and `/v2/commerce/listings` | Public | Current best prices and depth-based quotes. | Already exposed by `get_market_prices` and `quote_trade`. |
| `/v2/recipes` and `/v2/recipes/search` | Public | Recipe search and ingredient/output data. | Already exposed by `search_recipes` and `get_recipes`. |
| `/v2/items` | Public | Item metadata by ID. | Already exposed by `get_items`. The official route catalog lists `/v2/items` but no separate `/v2/items/search` route; name-query semantics remain to be checked when full endpoint documentation is available. |

The implemented inventory search covers bank, materials, shared inventory, and character bags. It does not include Trading Post delivery or legendary-armory entitlements. `find_items` batches requested IDs while reading each covered source once.

## Proposed next composable tools

These are research candidates, not approved implementation work yet.

1. **`get_guild_upgrades`** — metadata lookup against `/v2/guild/upgrades`. Keep it a direct lookup; do not resolve or recursively plan guild upgrade chains. Verify whether the endpoint supports ID batching, the response fields, and whether target recipes use `GuildUpgrade` ingredients before implementing it.
2. **`get_trading_post_delivery`** — direct read of `/v2/commerce/delivery`, with a deliberately small output containing item IDs/counts and coin proceeds if present. Useful only if uncollected delivery needs to be distinguished from owned inventory or available wallet funds. Confirm whether one endpoint response contains both items and coins, and what token scope it requires.
3. **`get_character_recipes`** — direct lookup of `/v2/characters/{escaped-name}/recipes`. Add only if the documented response represents character-specific knowledge that is not redundant with `get_account_recipes`; preserve recipe IDs and leave recipe details to `get_recipes`.
4. **`get_trading_post_transactions`** — expose documented current active orders only if the endpoint contract can distinguish them cleanly. This could prevent counting funds/items already committed to orders when estimating a plan. Do not assume `/v2/commerce/transactions` is a complete contract or invent `current`/`history` subpaths without documentation.
5. **`get_account_legendary_armory`** — optional account ownership lookup from `/v2/account/legendaryarmory`. Defer unless the workflow needs to distinguish an armory unlock from a physical item; it cannot certify trade eligibility of a particular instance.

The first two are the most plausible next additions. Keep them separate, ID-based, and endpoint-shaped. Do not add a one-call crafting planner, full catalog mirror, price optimizer, or generic API proxy.

## Remaining workflow gaps

- **Names to IDs:** users describe items by name, while the recipe and price tools take IDs. The route catalog does not expose a dedicated item search route. Do not download the full item catalog or build a name index as a shortcut. Verify `/v2/items` query semantics from the endpoint documentation when it returns; otherwise item IDs must come from the caller or a separately approved data source.
- **Recipe-tree valuation:** current tools return one recipe at a time. An agent can recurse through `search_recipes` and `get_recipes`, then use `get_items`, `get_market_prices`, and `quote_trade`. Alternate recipes, non-market inputs, recipe unlocks, character rating, existing holdings, and incomplete account sources must remain explicit in its comparison. This is analysis built from composable calls, not a hidden server-side plan.
- **Untradeable currencies:** recipe ingredient currency IDs can be resolved with `get_currencies`; account wallet amounts are already in `account_overview`. `find_items` handles item ingredients in bank/materials/shared inventory/character bags. Currency requirements and item requirements should not be combined into one numeric namespace.
- **Sale proceeds:** `quote_trade` estimates sale proceeds from current depth but does not observe existing open orders or pending delivery. The two commerce-account candidates above need documentation before their output can be used to adjust an estimate.
- **Recipe access:** `get_account_recipes` reports account recipe IDs. Whether the character-specific route adds nonredundant information remains open.

## Deferred research checklist

When the Guild Wars Wiki is back:

- Confirm exact response shapes and required key scopes for guild upgrades, character recipes, Trading Post delivery, and transactions.
- Confirm how `GuildUpgrade` recipe ingredients resolve and whether the type occurs in target recipes.
- Verify item endpoint name-search/filter parameters; do not infer support from the route catalog alone.
- Compare account-level and character-level recipe unlock semantics.
- Decide whether delivery/open-order state materially improves the target cost comparison enough to justify new tools.

## Sources

- Official GW2 API route catalog: https://api.guildwars2.com/v2.json
- Official GW2 API endpoint root: https://api.guildwars2.com/v2
- Existing tool contracts and exclusions: `IMPLEMENTATION.md`
