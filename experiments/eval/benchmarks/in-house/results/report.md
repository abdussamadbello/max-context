# Causal A/B Experiment — max-context vs grep

**Protocol hash (pre-registration):** `b5a83409f893df444f35b1557e6902ea4174d051070e3f16e263c36b8967b855`

**Task model:** us.anthropic.claude-sonnet-4-6  **Judge model:** us.anthropic.claude-haiku-4-5-20251001-v1:0 (different — no self-judging)

**Repos (pinned):** httpx@b5addb64f016 
**rg:** ripgrep 14.1.1 (rev 4649aa9700)  **started:** 2026-05-31T15:03:11Z  **replicates:** 1  **max turns:** 15

## Headline (paired, n=13 tasks; 0 excluded as infra failures)

- **Quality delta (mc − grep):** median +0.00, 95% bootstrap CI [+0.00, +0.00]
- **Cliff's delta (direction):** +0.00 (>0 favors max-context)
- **Wilcoxon signed-rank:** W+=0, n=0, z=0.00, two-sided p≈1.000
- **McNemar (fully-correct discordant):** mc-only wins=0, grep-only wins=0, χ²=0.00, p≈1.000
- **Token delta (mc − grep):** median -3276 tokens/task (negative = max-context cheaper)
- **Wall-clock delta (mc − grep):** median -2.7s/task (negative = max-context faster)
- **Tool-call delta (mc − grep):** median +0.0 calls/task (negative = max-context fewer)

> Honest scope: n is small. This is an existence+direction+effect-size result on these tasks/repos at pinned SHAs — NOT a universal law. Read per-task deltas and the failure table below.

## Per-task results

| Task | Type | grep | mc | Δ(mc−grep) | grep tok | mc tok |
|---|---|---|---|---|---|---|
| httpx-C01 | impact | 1.00 | 1.00 | +0.00 | 76138 | 5076 |
| httpx-C02 | impact | 1.00 | 1.00 | +0.00 | 10035 | 7809 |
| httpx-C03 | impact | 1.00 | 1.00 | +0.00 | 12281 | 5392 |
| httpx-C04 |  | 1.00 | 1.00 | +0.00 | 132093 | 7898 |
| httpx-C05 |  | 1.00 | 1.00 | +0.00 | 105364 | 7468 |
| httpx-E01 | edit | 1.00 | 1.00 | +0.00 | 12018 | 50308 |
| httpx-E02 | edit | 1.00 | 1.00 | +0.00 | 41537 | 26485 |
| httpx-G01 | edit 🌱grep-win | 1.00 | 1.00 | +0.00 | 107183 | 59499 |
| httpx-R01 | retrieval | 1.00 | 1.00 | +0.00 | 3183 | 3903 |
| httpx-R02 | retrieval | 1.00 | 1.00 | +0.00 | 3197 | 3771 |
| httpx-R03 | retrieval | 1.00 | 1.00 | +0.00 | 5049 | 6235 |
| httpx-R04 | retrieval | 1.00 | 1.00 | +0.00 | 10811 | 13635 |
| httpx-R05 | retrieval | 1.00 | 1.00 | +0.00 | 7237 | 3961 |

## Efficiency (time & cost) — grep / mc per task

Quality being ~tied, this is where any practical edge shows. Values are grep / max-context.

| Task | tool calls | tokens | wall-clock (s) |
|---|---|---|---|
| httpx-C01 | 16 / 2 | 76138 / 5076 | 51.7 / 10.8 |
| httpx-C02 | 2 / 2 | 10035 / 7809 | 17.1 / 17.0 |
| httpx-C03 | 4 / 2 | 12281 / 5392 | 16.9 / 12.6 |
| httpx-C04 | 21 / 2 | 132093 / 7898 | 70.1 / 25.6 |
| httpx-C05 | 14 / 2 | 105364 / 7468 | 61.6 / 14.9 |
| httpx-E01 | 4 / 18 | 12018 / 50308 | 20.4 / 51.9 |
| httpx-E02 | 5 / 12 | 41537 / 26485 | 23.5 / 35.4 |
| httpx-G01 | 20 / 19 | 107183 / 59499 | 79.3 / 61.0 |
| httpx-R01 | 1 / 1 | 3183 / 3903 | 5.1 / 6.0 |
| httpx-R02 | 1 / 1 | 3197 / 3771 | 4.7 / 6.1 |
| httpx-R03 | 2 / 3 | 5049 / 6235 | 13.5 / 10.8 |
| httpx-R04 | 6 / 6 | 10811 / 13635 | 20.2 / 20.9 |
| httpx-R05 | 4 / 1 | 7237 / 3961 | 11.0 / 6.4 |

## Failure / status breakdown (no silent drops)

| Status | grep | max-context |
|---|---|---|
| completed | 13 | 13 |

## Planted grep-win tasks

Tasks deliberately chosen to favor grep (prose/"why"/dynamic). Reporting these maps the boundary of where max-context helps.

- **httpx-G01**: grep 1.00 vs mc 1.00 (Δ +0.00)
