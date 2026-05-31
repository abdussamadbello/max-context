# Causal A/B Experiment — max-context vs grep

**Protocol hash (pre-registration):** `249ecc5e394932bf2a79302d2f8851e87141e0fba030b30b8529390b2355ed9e`

**Task model:** us.anthropic.claude-sonnet-4-6  **Judge model:** us.anthropic.claude-haiku-4-5-20251001-v1:0 (different — no self-judging)

**Repos (pinned):** celery@90e2a13cbe2d payments@bc194b5a9ecf 
**rg:** ripgrep 14.1.1 (rev 4649aa9700)  **started:** 2026-05-31T16:06:38Z  **replicates:** 1  **max turns:** 15

## Headline (paired, n=2 tasks; 0 excluded as infra failures)

- **Quality delta (mc − grep):** median +0.50, 95% bootstrap CI [+0.00, +1.00]
- **Cliff's delta (direction):** +0.50 (>0 favors max-context)
- **Wilcoxon signed-rank:** W+=1, n=1, z=1.00, two-sided p≈0.317
- **McNemar (fully-correct discordant):** mc-only wins=1, grep-only wins=0, χ²=0.00, p≈1.000
- **Token delta (mc − grep):** median -16626 tokens/task (negative = max-context cheaper)
- **Wall-clock delta (mc − grep):** median -20.3s/task (negative = max-context faster)
- **Tool-call delta (mc − grep):** median -7.5 calls/task (negative = max-context fewer)

> Honest scope: n is small. This is an existence+direction+effect-size result on these tasks/repos at pinned SHAs — NOT a universal law. Read per-task deltas and the failure table below.

## Per-task results

| Task | Type | grep | mc | Δ(mc−grep) | grep tok | mc tok |
|---|---|---|---|---|---|---|
| A01 | impact | 0.00 | 1.00 | +1.00 | 8576 | 4460 |
| A02 | impact | 1.00 | 1.00 | +0.00 | 33614 | 4477 |

## Efficiency (time & cost) — grep / mc per task

Quality being ~tied, this is where any practical edge shows. Values are grep / max-context.

| Task | tool calls | tokens | wall-clock (s) |
|---|---|---|---|
| A01 | 6 / 2 | 8576 / 4460 | 18.1 / 8.3 |
| A02 | 13 / 2 | 33614 / 4477 | 41.1 / 10.4 |

## Failure / status breakdown (no silent drops)

| Status | grep | max-context |
|---|---|---|
| completed | 2 | 2 |

## Planted grep-win tasks

Tasks deliberately chosen to favor grep (prose/"why"/dynamic). Reporting these maps the boundary of where max-context helps.

_(none in this protocol)_
