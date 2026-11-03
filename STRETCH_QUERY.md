# Last full T4 daily run and material earnings

Checked against live account data on November 3, 2026. The historical date and actual material earnings are **unknown from the available evidence**.

The narrow evidence retrieval took 1.926 seconds. It queried account achievement progress, resolved the Daily Fractals category from public metadata, and read that category's current achievement definitions. The current T4 objectives were Molten Boss, Twilight Oasis and Underground Facility; none had an entry in the returned account progress. Absence does not establish a past completion date or prove the account never completed them.

The 2,504 returned account achievement rows exposed `id`, `bits`, `current`, `max`, `done`, `repeated` and `unlocked`, with no completion timestamp. Account-wide `last_modified` is not a timestamp for completing a particular activity. Account progression and inventory ownership also do not provide an attributable record of the last full daily run.

There were no saved local session records in the configured default record storage. Current material holdings, later sales and expected chest rewards cannot prove what this particular run earned. Random rewards must not be substituted for actual loot.

The generic `activity_earnings_review` workflow supports this question when evidence exists. A caller-captured session record can link:

- Activity, complete objective set, reset window, and observed start/end times.
- Completion evidence and its source, including any uncertainty about exact timing.
- Earned item IDs and quantities, with unrelated trades, deposits, salvage and other activity reconciled.
- Historical captured prices, or current quantity-aware liquidation quotes clearly labeled as current valuation.

The existing record tools can store those observations when explicitly requested. No private gw2efficiency dataset is required, and current API state cannot recreate records that were never captured.

Sources: live official GW2 API responses, the [account achievement API contract](https://wiki.guildwars2.com/wiki/API:2/account/achievements), and the registered MCP workflow and record tools.
