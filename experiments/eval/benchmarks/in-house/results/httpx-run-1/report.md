# Causal A/B Experiment — max-context vs grep

**Protocol hash (pre-registration):** `ad11fa7f658edfa319371030a1b30720f31f2b6f606a6c059e80edb11330dfa8`

**Task model:** us.anthropic.claude-sonnet-4-6  **Judge model:** us.anthropic.claude-haiku-4-5-20251001-v1:0 (different — no self-judging)

**Repos (pinned):** httpx@b5addb64f016 
**rg:** ripgrep 14.1.1 (rev 4649aa9700)  **started:** 2026-05-31T14:33:24Z  **replicates:** 1  **max turns:** 15

## Headline (paired, n=11 tasks; 0 excluded as infra failures)

- **Quality delta (mc − grep):** median +0.00, 95% bootstrap CI [+0.00, +0.00]
- **Cliff's delta (direction):** +0.00 (>0 favors max-context)
- **Wilcoxon signed-rank:** W+=0, n=0, z=0.00, two-sided p≈1.000
- **McNemar (fully-correct discordant):** mc-only wins=0, grep-only wins=0, χ²=0.00, p≈1.000
- **Token delta (mc − grep):** median -3230 tokens/task (negative = max-context cheaper)
- **Wall-clock delta (mc − grep):** median -2.5s/task (negative = max-context faster)
- **Tool-call delta (mc − grep):** median +0.0 calls/task (negative = max-context fewer)

> Honest scope: n is small. This is an existence+direction+effect-size result on these tasks/repos at pinned SHAs — NOT a universal law. Read per-task deltas and the failure table below.

## Per-task results

| Task | Type | grep | mc | Δ(mc−grep) | grep tok | mc tok |
|---|---|---|---|---|---|---|
| httpx-C01 | impact | 1.00 | 1.00 | +0.00 | 98825 | 5099 |
| httpx-C02 | impact | 1.00 | 1.00 | +0.00 | 9722 | 6492 |
| httpx-C03 | impact | 1.00 | 1.00 | +0.00 | 10271 | 5426 |
| httpx-E01 | edit | 1.00 | 1.00 | +0.00 | 25913 | 27355 |
| httpx-E02 | edit | 1.00 | 1.00 | +0.00 | 41783 | 23952 |
| httpx-G01 | edit 🌱grep-win | 1.00 | 1.00 | +0.00 | 72693 | 64803 |
| httpx-R01 | retrieval | 1.00 | 1.00 | +0.00 | 3012 | 3842 |
| httpx-R02 | retrieval | 1.00 | 1.00 | +0.00 | 3060 | 3719 |
| httpx-R03 | retrieval | 1.00 | 1.00 | +0.00 | 9254 | 6145 |
| httpx-R04 | retrieval | 1.00 | 1.00 | +0.00 | 18719 | 10372 |
| httpx-R05 | retrieval | 1.00 | 1.00 | +0.00 | 6907 | 3918 |

## Efficiency (time & cost) — grep / mc per task

Quality being ~tied, this is where any practical edge shows. Values are grep / max-context.

| Task | tool calls | tokens | wall-clock (s) |
|---|---|---|---|
| httpx-C01 | 16 / 2 | 98825 / 5099 | 52.3 / 10.7 |
| httpx-C02 | 2 / 2 | 9722 / 6492 | 16.2 / 13.6 |
| httpx-C03 | 4 / 2 | 10271 / 5426 | 18.0 / 15.5 |
| httpx-E01 | 7 / 9 | 25913 / 27355 | 26.3 / 34.8 |
| httpx-E02 | 6 / 12 | 41783 / 23952 | 24.7 / 37.3 |
| httpx-G01 | 13 / 21 | 72693 / 64803 | 64.4 / 63.9 |
| httpx-R01 | 1 / 1 | 3012 / 3842 | 5.4 / 6.0 |
| httpx-R02 | 1 / 1 | 3060 / 3719 | 6.2 / 5.0 |
| httpx-R03 | 3 / 3 | 9254 / 6145 | 16.5 / 8.8 |
| httpx-R04 | 7 / 5 | 18719 / 10372 | 31.5 / 18.5 |
| httpx-R05 | 4 / 1 | 6907 / 3918 | 10.9 / 5.8 |

## Failure / status breakdown (no silent drops)

| Status | grep | max-context |
|---|---|---|
| completed | 11 | 11 |

## Planted grep-win tasks

Tasks deliberately chosen to favor grep (prose/"why"/dynamic). Reporting these maps the boundary of where max-context helps.

- **httpx-G01**: grep 1.00 vs mc 1.00 (Δ +0.00)
