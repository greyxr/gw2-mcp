# Fang versus Weight comparison

## Target question

Should I craft Aurene's Fang and sell it to fund Aurene's Weight, or craft Aurene's Weight directly? Use materials in my character bags, material storage, bank, and shared inventory first; buy missing tradeable inputs on the Trading Post. Compare standing listings/orders with instant transactions.

Every route must finish with one usable Aurene's Weight. The default indirect route is craft Fang, sell Fang, then craft Weight. Buying a finished Weight with Fang's proceeds is a separate alternative, to be labeled explicitly rather than substituted for crafting.

This is an analysis workflow for an MCP host using the server's composable tools. The workflow prompt supplies instructions, not an automated recipe engine or live result. It requires a host that follows the prompt, obtains any external acquisition data, and calls the tools. A natural-language question alone does not cause this server to perform the analysis.

## Required inputs and coverage

Resolve names to item IDs with `search_wiki`, then verify metadata with `get_items`. Do not download the full catalog. Use `search_recipes` and `get_recipes` for API recipe stages, and source-backed external data for acquisition stages not returned by the API, including Mystic Forge steps and vendors. An empty recipe search is not proof that an item cannot be crafted or has zero cost.

Search both final items and intermediate gifts/precursors before expanding missing quantities. An owned intermediate replaces its ingredients; do not also charge its full ingredient tree. Respect recipe output counts, batch rounding, alternate recipes, deterministic versus random yields, purchase limits, and account-bound requirements. Do not assume random outputs are guaranteed.

Use `find_items` in batches of at most 200 IDs and preserve source coverage. Each batch is a live observation, not an atomic account snapshot. Check wallet balances with `account_overview`, resolve currency IDs with `get_currencies`, and keep currency and item namespaces separate. Query crafting disciplines and recipe unlocks when relevant; lack of an account recipe ID alone does not establish that a Mystic Forge or automatically learned recipe is unavailable.

Nontradeable shortages are requirements to earn or acquire, not zero-cost TP purchases. Report their quantities and known acquisition constraints separately. Do not assign an arbitrary gold conversion to time, currencies, or bound resources.

## Inventory accounting

Start each independent route from the same observed holdings and wallet. Within an indirect route, consume Fang inputs first, credit the net sale at its actual stage, and use only the remaining inventory for Weight. Shared gifts, precursors, currencies, and base materials cannot be allocated twice. Carry crafted batch surplus forward within that route. Preserve residual inventory in the result so equal final weapons do not conceal unequal remaining assets.

For example, if there are 100 units of a shared material and each weapon needs 80, the direct route buys none. The Fang-then-Weight route consumes 160 and must buy 60. Subtracting the initial 100 independently from both weapon recipes would incorrectly price that route as requiring no purchases.

Report two separate views:

- Additional cash: purchases and fees less sale credits, with owned inputs costing no additional cash.
- Economic cost: additional cash plus a stated opportunity value for consumed tradeable holdings. Use a consistent, realizable net liquidation benchmark for both routes; do not count both an owned intermediate and its ingredients. Show unvalued bound inputs separately. This is not the same as replacement cost, which may be shown separately if useful.

Track balances after every stage. The maximum cash deficit across the sequence is the required starting gold; a profitable final balance does not prove the user can afford to craft Fang before selling it. Avoid counting both net sale proceeds and the listing fee again. With this server, net sale proceeds already deduct both seller fee estimates; delivery proceeds deduct only the exchange fee, and the listing fee is paid separately upfront.

## Market scenarios

Evaluate direct crafting under two procurement policies: instant buy and standing buy orders. For each, evaluate the indirect route under instant Fang sale and a standing Fang listing, producing four indirect scenarios. If finished-Weight purchase is requested, show it as a separate route family.

Use `get_market_prices` for benchmarks, and `quote_trades` for quantity-aware instant fills and seller fee estimates. Standing quotes require an explicit unit price: state the observed reference and chosen price, never silently assume an order fills. Use the same procurement and opportunity-value policy across routes being compared. Quotes from separate calls may change and do not reserve liquidity.

Never extrapolate beyond observed depth. An unfilled instant quote leaves an unresolved procurement or sale quantity, not a fully costed plan. Standing orders/listings remain conditional on filling and have no guaranteed completion time. Retain fee-rounding uncertainty. Check that Fang remains sellable before any account-binding action; public item metadata cannot certify the binding state of an owned instance.

## Expected answer

Lead with a recommendation only when the evidence supports one. Include a compact table with route, procurement mode, Fang sale mode, purchase cost, estimated sale net, additional cash, starting gold required, opportunity cost, and blockers. For each indirect scenario, show its difference from direct crafting under the same procurement policy.

Then give a shopping list with required/owned/consumed/buy quantities and storage locations, nontradeable shortages, remaining valuable holdings, source timestamps/coverage, and assumptions about standing prices, fill timing, recipe access, and fees. Mark incomplete scenarios explicitly. Do not claim a cheapest route when missing acquisition data, partial inventory, or inadequate liquidity could change the result.

## Current external-data evidence

The [Fang acquisition page](https://wiki-en.guildwars2.com/wiki/Aurene%27s_Fang) lists a Mystic Forge combination involving its weapon-specific gift and precursor plus Gift of Jade Mastery and Draconic Tribute. The [Weight gift page](https://wiki.guildwars2.com/wiki/Gift_of_Aurene%27s_Weight) shows the corresponding hammer combination with the same two shared components. These shared inputs make sequential allocation essential.

Earlier Wiki fetches returned HTTP 403, but live GW2 API access and an external recipe-source fallback were subsequently verified. The prior run is recorded in [AURENE_COMPARISON.md](AURENE_COMPARISON.md); its prices and holdings are historical observations. The current MCP prompt also checks consumables and Wizard's Vault purchase limits, supports batch quotes, and reports quote observation times. Current source availability must be checked on each run.

## Acceptance cases

- Direct Weight and indirect Fang-then-Weight finish with the same target quantity.
- Shared materials and currencies are consumed once per route, with leftovers carried between stages.
- Owned intermediates prevent charging their nested ingredients; batch outputs retain surplus.
- Every required currency remains in its own namespace and shortages block feasibility.
- All four sale/procurement combinations use explicit standing prices and fees exactly once.
- Insufficient instant liquidity, missing recipes, partial inventory, and missing unlock data remain visible.
- Positive eventual profit does not conceal insufficient starting gold.
- Cash savings and economic savings may disagree; the answer explains which metric drives its recommendation.
