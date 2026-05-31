# max-context vs grep — Causal A/B Findings

*Last updated: 2026-05-31. Supersedes the incremental notes in
`FINDINGS-archive-*.md`. Written fresh from the 7-run dataset in `results/`.*

**Question:** Does an LLM understand and edit code *better* with max-context's
MCP tools than with grep + read_file?

---

## Method

- **Two arms, one variable.** Same task model (Claude Sonnet 4.6), same prompt
  skeleton, same turn budget, temperature 0. The only difference is the toolset:
  - **grep arm** — real ripgrep + `read_file` (ranges/whole) + `list_files`. It
    chooses its own search terms (no pre-supplied terms; not a strawman).
  - **max-context arm** — the real tools over **stdio MCP** to the actual
    `max-context` binary (`get_definition`, `query_codebase`, `get_call_chain`,
    `get_impact`, `get_architecture`).
- **Grading.** Retrieval ("where is X?") → objective file-set F1 + a lead-file
  check, vs a hand-verified key from an **independent** oracle (ripgrep + manual;
  never max-context's own output). Relationship ("all callers of X") → symbol
  recall vs a hand-verified caller set. Edit ("where/how to change X") → a
  **different** model (Claude Haiku 4.5) scoring a pre-registered rubric (no
  self-judging). Infra failures are excluded from quality, shown separately.
- **Backends.** Anthropic API and AWS Bedrock (same agent loop). Later runs used
  Bedrock — higher throughput, ~3× faster than the rate-limited API key.
- **Scope.** 1 repo (httpx @ `b5addb64`), 8→11 tasks, single replicate/run, 7
  runs. **Small n.** These are direction + effect-size results on a defined
  sample, **not** a universal verdict. Protocol (tasks/keys/rubric) is
  pre-registered; the hash is in every run's manifest.

---

## TL;DR

1. **Started worse than grep.** In a real agent loop max-context *lost*: it
   looped on every edit task (never answered) and used more tokens.
2. **Now ties on quality, wins on cost for graph tasks.** After fixing the tools
   (not the model), max-context matches grep's answer quality on every task type,
   and on relationship tasks ("list all callers") it is **66–84% cheaper and
   2–3× faster**.
3. **The decisive fix was a correctness bug, not prompting:** the call graph was
   **blind to inherited methods**. Fixing it turned max-context's worst task into
   its best.

---

## The arc, run by run

### Runs 1–2 — the claim failed
- **Edit tasks: max-context looped and never answered** (`truncated_turns` on
  E01/E02/G01); grep answered all of them correctly.
- **Tokens: grep cheaper** (e.g. R01: grep 3,012 vs mc 16,910).
- Root cause (transcripts): max-context returned verbose ranked lists; the model
  read them as "search again" and called `query_codebase` 20–28× without
  committing. grep's terse `path:line:text` gave a clean "found it, stop" signal.

### Runs 3–5 — fixing the loop
Tool-layer changes (model untouched): a new **`get_definition`** (one decisive
`file:line` + an "answer now" field), **terser `query_codebase`** (fewer results,
capped snippets, exact-match lead), **`get_impact` steering** + a server-side
nudge after repeated searches. Outcome: the edit-task loop disappeared and
**stayed gone across runs 4–5** — max-context completed all edit tasks and tied
grep (5/5, 5/5, 3/3), including the task planted to favor grep.

### Run 6 — "tie → better" attempt: still a tie
Added relationship tasks (C01–C03: "list all callers", "blast radius") — grep's
structural blind spot — and fixed the retrieval grader (see below). Result:
**quality tied** (Cliff's δ=0.00, every per-task Δ=0.00), max-context slightly
cheaper overall, but relationship-task savings were **inconsistent** (+79%, +25%,
−103%). The −103% (C01: 16 tool calls, 52k tokens) demanded a root cause.

### Run 7 — the inheritance fix lands the win
**Root cause of C01's blow-up:** `get_call_chain("build_request")` returned
**empty**. `self.build_request()` is called inside `Client.request`, but
`build_request` is defined on the *parent* class `BaseClient` — and the resolver
didn't walk inheritance, so every `self.<inherited>()` call was `unresolved`. The
model asked the right tool, got nothing, and looped.

**Fix:** capture class base-classes (schema `migrationV5` → `class_bases`) and
walk the inheritance chain in the resolver's `methodOnType`. Now
`self.<inherited>()` resolves.

**Effect on httpx (run 6 → run 7), max-context arm on C01:**

| | calls | tokens | wall-clock |
|---|---|---|---|
| run 6 (no inheritance) | 16 | 51,637 | 47s |
| run 7 (inheritance) | **2** | **5,101** | **11s** |

Same correct answer — one decisive call instead of 16.

---

## Where things stand now (run 7, n=11, 0 infra failures)

**Quality: tied across the board.** Cliff's δ=0.00; every paired per-task
Δ=0.00. max-context matches grep on retrieval, relationship, and edit tasks.

**Cost & time: max-context wins on relationship tasks — now consistently:**

| Task (all callers of…) | grep tokens | mc tokens | mc saves | calls g/mc | wall g/mc |
|---|---|---|---|---|---|
| C01 build_request | 32,319 | 5,101 | **84%** | 10 / 2 | 33s / 11s |
| C02 _build_auth_header | 19,163 | 6,449 | **66%** | 6 / 2 | 29s / 13s |
| C03 _merge_url | 16,947 | 5,441 | **68%** | 7 / 2 | 28s / 15s |

This is the structural advantage made real: grep must search, read many files,
and hand-exclude docstring/comment noise; the resolved call graph answers in
~2 calls. **Recall tied (both 1.00)** — but max-context reaches it far cheaper.

---

## The grader fix (why retrieval F1 looks low but isn't a loss)

Retrieval F1 for max-context still reads 0.50–0.67 on overloaded names (R01
`URL`, R04 `Timeout`). The transcripts show the **answer is correct** — e.g. R01:
"URL is defined in `httpx/_urls.py`; the `url` in `_models.py`/`conftest.py` are
methods, not the class." The objective grader (`ExtractFiles`) counted every path
mentioned, so files named only to be **dismissed** deflated precision.

**Fix:** grade the **lead file** (the one the answer endorses, first-mentioned),
not every path. Verified: R01 run 7 → `lead_correct = true` even though raw
F1=0.50. So the residual retrieval "gap" is largely a **measurement artifact**;
the report's quality metric now credits a correct lead.

---

## Honest caveats

- **n is small** (8–11 tasks, 1 repo, 1 replicate). Quality deltas are not
  statistically significant — the credible claims are qualitative (loop fixed;
  relationship-task cost/time win) plus the large within-task efficiency deltas.
- **Quality is a tie, not a win.** max-context is *as accurate* as grep and
  *cheaper/faster* on graph tasks. "Strictly better on quality" is unproven and
  would need harder targets (high fan-in, dynamic dispatch, renamed imports) and
  more repos.
- **Edit-task token cost is noisy** and sometimes higher for max-context
  (run 7 E02: mc 51k vs grep 42k). The loop is fixed; raw cost on diffuse edits
  still varies run to run.
- **Inheritance fix is partial:** single/normal inheritance resolves; calls on
  unrelated classes, locals from returns, and dynamic dispatch remain
  `unresolved` by design (no false edges).
- **One model.** Behavior is a model×tool-design interaction; another model may
  differ.

---

## How the harness kept itself honest

Four confounds were caught and fixed mid-experiment — each would have faked a
result if undetected:

- **`rg` was a shell function, not a binary** → grep arm silently errored on
  every call (run 1). Fixed: explicit `--rg` path + startup preflight.
- **Rate-limit death / budget runaway** → per-task checkpointing, exact
  `Retry-After`, `budget_halt` guardrail.
- **Judge model invalid on Bedrock** → recorded `gradeerr`, fixed with explicit
  `--judge-model` (Bedrock ids are inconsistent: sonnet unversioned, haiku
  versioned).
- **Grader counted dismissed files** → lead-file scoring (above).

Every run ends with an explicit status; nothing is dropped silently.

---

## What actually moved the needle (engineering takeaways)

1. **Inherited-method resolution** (`class_bases` + chain walk) — biggest single
   win; the call graph was blind to the dominant OOP call pattern.
2. **A decisive single-answer tool** (`get_definition`) + terser output — killed
   the edit-task query loop.
3. **Honest grading** (lead-file, infra-exclusion, recall for relationship) —
   revealed that several apparent "losses" were measurement artifacts.

The token claim should still be scoped honestly: max-context's "fewer tokens"
holds **per agent session on graph/relationship tasks** (66–84% here), not as a
blanket per-call multiple.

---

## Next steps to go from tie → demonstrably better

1. **Harder relationship targets** — high fan-in (20+ callers), renamed imports,
   dynamic dispatch — where grep's recall *drops* and max-context's holds. That's
   where a quality (not just cost) win should appear.
2. **Scale repos** — flask / got / zod — for cross-repo n (cheap via Bedrock).
3. **Freshness test** — edit a file mid-session, then query it: max-context's
   <2s watcher re-indexes; a grep agent on stale reads is wrong. A
   correctness-over-time advantage no static baseline can win. Untested so far.

---

## Reproduce

See `README.md`. Each `results/httpx-run-{1..7}/` holds the machine-readable
`results.json` (manifest + per-run records), `report.md`, and full transcripts.
A third party can re-grade with their own judge or re-run with their own prompts.
