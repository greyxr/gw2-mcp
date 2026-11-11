# Scenario coverage

This audit follows the public navigation on [gw2efficiency.com](https://gw2efficiency.com/) (Tasks, Account, Stats, Currency, Tradingpost, Timers, Instances, Crafting, Gathering, and Lottery). It describes workflows possible from public game data, the user's own authorized API key, and caller-captured local records. Private gw2efficiency account processing, historical datasets, ranking baselines, proprietary valuation, and site calculations are excluded by scope, not pending features. No parity claim is made.

## How to read this matrix

- **Typed** means a dedicated MCP tool returns a defined result shape for the named data. The result still has the limits described in that tool's output.
- **API read** means discover the available official API endpoint with `list_api_endpoints`, then make a narrow `query_api` call. This is raw source access, not a complete page, calculator, rank, or planner.
- **Reference/source** means `get_reference` can retrieve bounded external text from allowlisted pages, including Guild Wars 2 Wiki references. It cannot establish account ownership or prove every game mechanic.
- **No built-in history** means local records can preserve caller-supplied notes or captured observations, but the server does not reconstruct earlier state, price changes, or logs. Existing game API history endpoints, when they exist, are distinct and need their own sourced/permission checks.
- **Not established** marks features requiring unavailable public endpoints, unverified mechanics, or unprovided public data. Private gw2efficiency datasets and calculations are explicitly excluded, not backlog items.

## Menu-to-capability matrix

| gw2efficiency menu scenario | MCP coverage and boundary |
| --- | --- |
| Tasks: Daily | `earning_route_review` reads discovered daily/task definitions and account progress where exposed, then separates deterministic completion rewards from random rewards. No reset inference or guaranteed reward calculation. |
| Tasks: Wizard's Vault | `earning_route_review` reads exposed current reward listings and per-account purchase counts, then accounts for Astral Acclaim costs and balance. Rewards are limited purchases, not free or unlimited; no reward claiming. |
| Account: Overview | Typed `account_overview` reads identity, token permissions, and wallet. It is not the site's account dashboard or valuation. |
| Account: Characters | Typed `list_characters` and `get_character_crafting`; `account_audit` can read `characters/:id/inventory` after resolving a listed character. |
| Account: Equipment and builds | `account_audit` can read `characters/:id/equipment` after resolving a listed character. Equipment is separate from bags and not available crafting stock; no build optimizer is built in. |
| Account: Guilds | `account_audit` can query discovered guild membership, upgrades, and stash routes with required key permissions. Guild storage must be read and deduplicated separately from personal inventory. No site guild dashboard. |
| Account: PvP | `account_audit` can query discovered PvP account/player routes. Match history, rating, leaderboards, and derived PvP statistics are not part of typed tools or guaranteed by API data. |
| Account: Achievements | `unlock_collection_review` can combine discovered achievement definitions and account progress. No complete site progress dashboard. |
| Account: SAB | `unlock_collection_review` can review exposed Super Adventure Box achievement/unlock data. It does not include a site-specific collection score. |
| Account: Farming | `earning_route_review` can compare sourced rewards and marketable items. No automatic farm route or unverified drop-rate/gold-per-hour model. |
| Account: Time Machine | Public/current account data can be analyzed against caller-captured local records. Historical datasets held privately by gw2efficiency are excluded; current reads cannot recreate past account state. |
| Account: Wallet | Typed `account_overview`; `get_currencies` resolves currency metadata. `currency_conversion_review` checks sourced exchange/vendor/reward options without assigning unsupported values. |
| Account: Bank | Typed `find_items` search bank alongside their other defined sources. Failures are reported and totals may be lower bounds. |
| Account: Material Storage | Typed `find_items` includes material storage. It does not show every account-held item source. |
| Account: Shared Inventory | Typed `find_items` includes shared inventory. |
| Account: Wardrobe | API read for unlock endpoints where available; not a rendered wardrobe or a complete owned-item inventory. |
| Account: Miniatures | API read for unlock endpoints where available; no mini collection scoring. |
| Account: Mounts | API read for unlocks where available; no mount progression planner. |
| Account: Dyes | API read for unlocks where available; no complete dye collection valuation. |
| Account: Recipes | Typed `search_recipes`, `get_recipes`, and `get_account_recipes`; recipe definitions, account unlocks for the checked recipe IDs, and current character craftability remain distinct. |
| Account: PvP Heroes | API read only where relevant endpoints exist. Site-specific hero completion/score is not established. |
| Account: Mailcarriers | API read for unlocks where available. |
| Account: Outfits | API read for unlocks where available. |
| Account: Finishers | API read for unlocks where available. |
| Account: Homestead | API read for exposed unlock/account data; no complete homestead inventory or progression model is built in. |
| Account: Gliders | API read for unlocks where available. |
| Account: Novelties | API read for unlocks where available. |
| Account: Skiffs | API read for unlocks where available. |
| Account: Emotes | API read for unlocks where available. |
| Account: Jadebots | API read for unlocks where available. |
| Account: Fishing | API read for exposed unlock/account data. Fishing route and catch probabilities need sourced evidence. |
| Account: Upgrades | API read for account/recipe data where exposed. This is not a universal view of equipped upgrades or guild upgrades. |
| Account: Legendary Armory | API read for exposed entitlement data. Armory unlocks are not item quantities in the searched inventory sources. |
| Account: Relics | API read for unlock data where available. |
| Stats: Account Statistics | `account_value_and_history` can total explicitly covered live sources and timestamped saved records. Site-defined aggregates and full coverage are not claimed. |
| Stats: Unlock Statistics | `account_value_and_history` can total sourced unlock data. Site filters, categories, denominator, and completion percentages may differ and are not reproduced. |
| Stats: Player Statistics | `account_value_and_history` can analyze public API results or caller-captured records that the user supplies. Private gw2efficiency populations and ranking baselines are excluded, not missing implementation. |
| Stats: Skin Statistics | `unlock_collection_review` can review account skin unlock routes; `account_value_and_history` can total covered public unlock data. Private rarity and ranking baselines are excluded. |
| Currency: Dungeon Delving | `account_overview` reads wallet amounts; `get_currencies` resolves IDs. `currency_conversion_review` needs a sourced spending route before valuing it. |
| Currency: Spirit Shards | `account_overview` reads wallet; `currency_conversion_review` can compare sourced routes and listed costs/limits. |
| Currency: Karma | `account_overview` reads wallet; `currency_conversion_review` can compare sourced routes and listed costs/limits. |
| Currency: Badges of Honor | `account_overview` reads wallet; `currency_conversion_review` can compare sourced routes and listed costs/limits. |
| Currency: Gems | `account_overview` reads wallet. `query_api` supports `commerce/exchange/gems`; `currency_conversion_review` can report requested exchange amount and observation time. No regional cash price or site-derived gem valuation. |
| Currency: Laurels | `account_overview` reads wallet; `currency_conversion_review` can compare sourced routes and listed costs/limits. |
| Currency: Guild Commendations | `account_overview` reads wallet; `currency_conversion_review` can compare sourced routes and listed costs/limits. |
| Currency: Claim Tickets | `account_overview` reads wallet; `currency_conversion_review` can compare sourced routes and listed costs/limits. |
| Currency: Exchange and conversion routes | `query_api` supports `commerce/exchange/coins` and `commerce/exchange/gems`; `currency_conversion_review` covers sourced vendor, reward, or item-mediated routes. No universal currency-to-gold table. |
| Tradingpost: Overview | Typed `get_market_prices` and `quote_trades` cover public current prices/depth. Account orders and delivery are separate reads. |
| Tradingpost: Delivery Box | `trading_post_review` can query account delivery only where the discovered route and key permission allow it. The server cannot collect delivery. |
| Tradingpost: Current Transactions | `trading_post_review` can query `commerce/transactions/current/buys` and `/sells` with required permission. It does not normalize a site portfolio view. |
| Tradingpost: Transaction History | `trading_post_review` can query `commerce/transactions/history/buys` and `/sells` with pagination; the API keeps about 90 days. Missing history is not reconstructed from current state. |
| Tradingpost: Investment History | No built-in investment ledger. `trading_post_review` can reconcile available transaction history; site-specific profit accounting is not implemented. |
| Timers | `scenario_workbench` can read current public timer/event routes returned by endpoint discovery; cite the route and `observed_at`. No background polling, notifications, or reset guarantees. |
| Instances: Fractals | API read for exposed progress/unlocks; reward expectations and rotations need current source evidence. |
| Instances: Raids | API read for exposed progress/unlocks; no encounter planner, group eligibility engine, or complete reward model. |
| Instances: Dungeons | API read for exposed achievement/account data; dungeon currency and reward planning need separate evidence. |
| Crafting: Recipe Search | Typed recipe search by output or ingredient and typed recipe details. Search results are API recipes, not a guarantee that every acquisition method is represented. |
| Crafting: Calculator | `crafting_or_profit_plan` guides a host-calculated bounded recipe tree with market reads. `quote_trades` batches independent quotes; the server has no recursive recipe optimizer or atomic inventory allocator. |
| Crafting: Legendaries | Crafting prompt can analyze a named target using API recipes plus get_reference-cited acquisition steps. A complete legendary catalog/acquisition graph is not included. |
| Crafting: Precursors | API read for available recipes/items; source-backed external steps may be needed. No precursor price or collection completion database is built in. |
| Crafting: Ascended Gear | API recipe and market primitives support bounded analyses. Account/character access, selectable stats, and acquisition alternatives must be verified separately. |
| Crafting: Zephyrite Supply Boxes | Reference/source plus API item/market data can support a bounded comparison when current box contents are sourced. Random yield is not guaranteed; no drop simulator is built in. |
| Gathering | `earning_route_review` can compare a route only where current requirements, timing, yield, and value have cited public API or reference evidence. No private site dataset, map-wide optimizer, or guaranteed node yield. |
| Lottery | Private site lottery data is excluded by scope. A public, sourced lottery record can be analyzed if the user supplies it, but no private dataset, odds, or site computation is reproduced. |

## Reusable workflow prompts

MCP prompt listing is the source of truth for current names and required arguments. The prompt set is:

| Prompt | Intended workflow |
| --- | --- |
| `aurene_fang_or_weight` | Detailed stage-aware Fang sale versus Weight craft comparison, including four procurement/sale combinations and optional finished-item purchase. |
| `account_audit` | Scoped account, wallet, inventory, equipment, and permission audit. |
| `unlock_collection_review` | Achievement, wardrobe, mount, mastery, homestead, and related unlock goal. |
| `trading_post_review` | Open orders, delivery, current transactions, history, or proposed trade review. |
| `crafting_or_profit_plan` | Bounded recipe, shopping, or crafting-profit analysis. |
| `earning_route_review` | Daily tasks, Wizard's Vault, instance, farming, or gathering route comparison. |
| `activity_earnings_review` | Evidence-based last-completion time and actual item/gold earnings for any activity, with current progress separated from history. |
| `currency_conversion_review` | Sourced vendor, exchange, reward, or item-mediated currency conversion with distinct units and no arbitrary values. |
| `account_value_and_history` | Scoped account value/statistics report with strict live-versus-history separation. |
| `scenario_workbench` | User-defined workflow reduced to a small sourced read plan. |

Each prompt asks the host to discover only relevant endpoints, preserve source and time context, and label unsupported evidence. Prompt retrieval does not call the APIs. The host model still needs to perform the reads and calculations, so correctness depends on its compliance with those instructions and the quality/availability of sources.

## Known global boundaries

1. Generic endpoint access makes additional public API reads possible, but does not provide site-level schemas, account-wide completeness, normalized calculations, or source coverage guarantees by itself. Private gw2efficiency account pages, processing, and datasets are outside scope.
2. Item search covers bank, material storage, shared inventory, and character bags. It excludes equipment/socketed upgrades, legendary-armory entitlements, guild storage, Trading Post delivery, and pending orders unless separately read.
3. Live endpoint calls are separate observations, not an atomic account snapshot. Public Trading Post depth can change and does not reserve liquidity. Fee rounding is an estimate.
4. Local records are caller supplied. They can preserve explicitly captured observations but do not add a background time machine or verify their provenance. Private historical datasets and population ranking baselines are excluded by scope.
5. get_reference can return Wiki pages documenting acquisition steps, item behavior, or reward mechanics absent from the API. They are external evidence, not an authoritative record of this account.
6. For public-data analysis, no rank, history, profit, or completion claim should be made unless its population, dates, denominator, acquisition sources, and endpoint coverage are available. This does not imply access to, or scope to recreate, private gw2efficiency datasets.
7. Historical datasets, population rankings, private account-page processing, and lottery datasets from gw2efficiency are excluded by scope. Public API facts and user-supplied public observations remain analyzable; that does not recreate private baselines or calculations.
