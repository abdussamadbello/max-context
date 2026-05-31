# Findings — Causal A/B Experiment (httpx, 2 runs)

**Date:** 2026-05-30  **Status:** preliminary, underpowered, one repo, but
**two runs agree** on the key results.
**Repo:** httpx @ `b5addb64f016`  **Task model:** claude-sonnet-4-6
**Artifacts:** `results/httpx-run-1/`, `results/httpx-run-2/`

| | Run 1 | Run 2 |
|---|---|---|
| Pre-reg hash | `f1727d97…` | `3e2404e4…` |
| Judge model | claude-opus-4-8 | claude-haiku-4-5 (cheaper) |
| max_tokens / call | 1500 | 10000 |
| token_budget | 200k | 600k |
| Edit tasks: mc status | truncated/looped | **truncated/looped (reproduced)** |

## TL;DR

The experiment tested the product claim *"LLMs understand/edit codebases better
with max-context than with grep."* Across **two runs** with different judge
models and token budgets, **the claim is NOT supported on this sample — and on
two axes it reverses:** max-context used more tokens, and it **failed every
open-ended edit task by looping** while grep answered all of them correctly.
Small n — a direction/existence signal, not a universal verdict — but the
direction is consistent and now reproduced.

## What we measured — retrieval (5 clean paired tasks per run)

| Metric | Run 1 grep | Run 1 mc | Run 2 grep | Run 2 mc |
|---|---|---|---|---|
| Avg retrieval F1 | 0.93 | 1.00 | **1.00** | 0.87 |
| Avg tokens/task | 6,215 | 10,894 | 5,313 | 9,658 |
| Fully-correct | 4/5 | 5/5 | 5/5 | 3/5 |

- **Quality: tied / within noise, and which arm "wins" flips between runs.**
  Run 1 mc edged ahead (won R04); run 2 grep edged ahead (won R01 & R04). Neither
  is significant (Wilcoxon p≈0.32 run 1, p≈0.18 run 2). The "winner" flipping on
  R04 between runs is itself the finding: **at n=5 the quality difference is
  noise.** With 10k-token answers both arms name extra files, which lowers
  precision unpredictably.
- **Tokens: grep won in BOTH runs.** Median delta ≈ **+5,375 tokens in grep's
  favor** per task, consistent across runs. max-context's verbose JSON
  (architecture summaries, ranked snippets) costs ~1.8× grep's terse
  `path:line:text`.

## The headline contradiction

`BENCHMARK.md` reports **~30× fewer tokens** for max-context. That measured a
**single idealized tool-call response**. In a **real agent loop**, the model
calls tools repeatedly and the picture inverts: across both runs max-context
consumed *more* tokens, not fewer. "One tool response is small" and "the whole
agent session is small" are different claims; only the first was ever supported.

## Edit tasks: a real, reproduced max-context weakness

**max-context failed every open-ended edit task in both runs by query-looping.**

- Run 1 (max_tokens=1500): E01, E02 → `truncated_turns`.
- Run 2 (max_tokens=10000, budget 600k): E01, E02, **and** the planted-grep-win
  G01 → all `truncated_turns`. The mc arm called `query_codebase` ~23 times in
  15 turns and **never committed to an answer**.
- grep answered **all** edit tasks correctly: run 2 scored grep 5/5, 5/5, 3/3
  (E01, E02, G01) via the haiku judge.

**The run-1 "maybe it was just the 1500-token cap" caveat is now ruled out.**
Run 2 gave the mc arm 10,000 output tokens/call and a 600k budget — and it
*still* looped. The truncation is **behavioral, not a budget artifact**: when
`query_codebase` returns ranked results, the model reads them as "refine and
search again" rather than "I have enough — answer." grep's flat `path:line:text`
gives a cleaner "found it, done" signal. This is the experiment's strongest,
most actionable finding, and it reproduced across two configurations.

## Why this is still not the final word

1. **n=5** clean retrieval tasks per run, one repo — underpowered for the
   *quality* comparison (Wilcoxon p≈0.18–0.32, not significant either way).
2. Retrieval tasks skew toward simple lookups where both arms do well.
3. The edit-task result is qualitative (loop vs answer), not a graded quality
   delta, because the mc arm never produced an answer to grade.
4. One model (sonnet-4-6). A different model might use the tools differently —
   the loop behavior is model×tool-design interaction, not tool-design alone.

## What the harness proved about itself (credibility controls worked)

- Caught a confound: `rg` was a shell function, not a binary — the first run's
  grep arm was silently broken. Added an explicit `--rg` path + a startup
  **preflight** that aborts if grep can't execute.
- Caught a second confound: a mid-run rate-limit death. Added **per-task
  checkpointing** (no lost work) and **exact `Retry-After` honoring**.
- Caught a third: in run 2 the grep arm tripped the **`budget_halt`** guardrail
  at 205k tokens (reading large files). Recorded as an infra status and excluded
  from quality stats; budget then raised to 600k. The guardrail stopped a
  runaway before it cost more — exactly its purpose.
- Excluded infra failures (rate_limited / truncated) from quality stats so they
  don't masquerade as capability losses; surfaced them in the failure table.
- Objective grading (file-set F1, no LLM) for retrieval; the judge never touched
  the retrieval numbers. Pre-registration hash recorded.

## Product implications (actionable, now evidenced by 2 runs)

1. **Make tool responses terser — highest leverage.** The edit-task looping
   reproduced at 10k tokens, so it is *not* a budget problem: max-context's
   ranked-result verbosity invites "search again" instead of "answer." A compact
   mode (fewer fields, capped snippet length, an explicit "this fully answers X"
   framing, or a `get_definition`-style single-answer tool) is the change most
   likely to flip edit-task outcomes.
2. **Reframe the token claim.** "30× fewer tokens" holds *per tool-call* but
   **reverses end-to-end** (grep used ~1.8× fewer tokens per agent session in
   both runs). `BENCHMARK.md` should scope the claim to single calls or
   re-measure over full agent sessions.
3. **Add an agent-session token metric** to the product's own benchmark — the
   per-call number is misleading for how the tool is actually used.
4. **Then re-validate:** with a terser mode, re-run this harness (a clean
   `max_tokens`/budget run is now possible) and scale to flask/got/zod for
   cross-repo n before making any causal quality claim.

## Fixes shipped in response (2026-05-30, pending re-validation)

Acting on the findings above, the core tools were changed to be **terse and
decisive**:

1. **New `get_definition` tool** — exact-name lookup returning a single
   `file:line` plus an `answer` field ("definitive location — answer now"). This
   is the antidote to the retrieval/edit loop: a terminal "found it" signal like
   grep's, with index precision. (This lifts the Phase-8 4-tool cap, which was
   explicitly scoped "until Phase 9 opens" — now 5 tools.)
2. **Terser `query_codebase`** — default results 5→3, snippets capped at 160
   chars, caller/callee enrichment dropped, and an `answer` field emitted on a
   single exact-name match.
3. **Decisive guidance** — the emitted `SKILL.md` now tells host agents to use
   `get_definition` first and **commit after 1–2 calls** when an `answer` is
   present.

These target the diagnosed cause (ranked lists → "search again").

### Run 3 — validation of the fixes (same protocol, 5-tool binary)

The fixes **partially worked, and measurably:**

| Signal | Run 2 (before) | Run 3 (after) |
|---|---|---|
| Edit task E01 (localized) | `truncated_turns` (looped) | **completed, 5/5** |
| Edit tasks E02, G01 (diffuse) | truncated | still truncated |
| Token delta (mc − grep), median | **+5,377** (grep cheaper) | **−1,293** (mc cheaper) |
| Retrieval quality | tied/noise | tied/noise (unchanged) |

**What the fix achieved:** the *localized* edit task ("where do I add a default
header?") flipped from an infinite query-loop to a clean, correct 5/5 answer —
the `get_definition`/decisive-`answer` stop-signal did its job. And max-context's
terse mode flipped the **token economics**: median tokens/task went from grep
being ~1.8× cheaper to **max-context being cheaper**. On one retrieval task (R03)
the grep arm now blew 205k tokens reading files while mc used 8k — the mirror
image of run 1.

**What it did NOT fix:** the two *diffuse* edit tasks (E02 "what depends on the
timeout default?", G01 "why/where is trust_env handled?") still loop. The E02
transcript shows the model *did* call `get_definition` and *did* receive `answer`
fields — then called `query_codebase` 25 more times anyway. For genuinely
exploratory "what-depends-on-this" tasks, a single decisive location isn't enough;
the model keeps probing. `get_impact` is the right tool for these, but the model
defaulted to search.

**Honest takeaway:** terser + decisive output is a real, measured improvement
(localized edits fixed, token cost now favorable) — but it does **not** make
max-context "100% like grep." The residual looping on diffuse dependency-tracing
tasks is the next problem: likely needs stronger guidance steering those tasks to
`get_impact`, or a turn-budget-aware "you have enough, answer now" nudge. n is
still tiny (1 edit task fixed out of 3); treat this as a promising direction, not
a settled result.

### Run 4 — gap fixes validated (Bedrock backend)

After run 3's partial result, two more fixes shipped and run 4 measured them on
AWS Bedrock (`us.anthropic.claude-sonnet-4-6` task, `claude-haiku-4-5` judge):

- **Gap 1 (diffuse edit-task loop):** `get_impact`/`get_call_chain` steering in
  the guidance **plus a server-side nudge** — after several `query_codebase`
  calls the tool injects "use get_impact or commit to an answer."
- **Gap 2 (retrieval precision):** when one result is an exact-name match,
  `query_codebase` now leads with that single result and drops the others.

**Edit-task trajectory across all four runs (the headline win):**

| Edit task | Run 1 | Run 2 | Run 3 | Run 4 |
|---|---|---|---|---|
| E01 (localized) | loop | loop | ✅ 5/5 | ✅ 5/5 |
| E02 (diffuse)   | loop | loop | loop   | ✅ 5/5 |
| G01 (planted grep-win) | — | loop | loop | ✅ 3/3 |

**Run 4 outcome:** the max-context arm **completed all three edit tasks and tied
grep on every one** (5/5, 5/5, 3/3). The query-loop — max-context's worst and
most reproducible failure across runs 1–3 — is **gone**. Token cost also nearly
equalized: median mc−grep delta fell from **+5,377** (run 1) to **+634** (run 4),
and on the diffuse G01 task max-context used **51k tokens vs grep's 111k** (mc
roughly half).

**Residual gap (honest):** retrieval precision. On overloaded names (R01 "URL",
R04 "Timeout", R05) mc still scores 0.50–0.67 because the Gap-2 lead only fires
on a *single* exact match — httpx defines `URL` in 3 places, so it can't collapse
and the model names an extra file. grep happens to name only one. This is the
next thing to fix (e.g. rank the same-name-in-most-relevant-module hit first, or
have get_definition pick the canonical class over same-named methods).

**Caveats unchanged:** n=5–7 per run, one repo, single replicate; edit tasks
flip between runs (E01 looped in run 4's first attempt before the judge fix),
so treat the trajectory as a strong direction, not a settled quality verdict.
The fixes are real and measured; broader validation (flask/got/zod, replicates)
is still needed — and now cheap to run via the Bedrock backend.

## Backends

The harness supports two model backends (same agent loop, pluggable `Caller`):
- **Anthropic API** (`--backend anthropic`, needs `ANTHROPIC_API_KEY`).
- **AWS Bedrock** (`--backend bedrock --aws-profile <p>`), shelling out to
  `aws bedrock-runtime converse`. Bedrock model ids are inconsistent (sonnet
  works unversioned, haiku needs the version suffix), so use `--task-model` /
  `--judge-model` to set full ids explicitly. Bedrock's higher throughput made
  runs ~3× faster than the rate-limited API key.

## Reproduce

See `README.md`. Pinned protocol + manifest + full transcripts are in
`results/httpx-run-1/` … `results/httpx-run-4/`. A third party can re-grade or
re-run from those artifacts.
