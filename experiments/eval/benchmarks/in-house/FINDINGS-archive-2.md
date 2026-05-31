# max-context vs grep — Causal A/B Findings

**Question:** Does an LLM understand and edit code *better* with max-context's
MCP tools than with grep + read_file?

**Method:** Same model, same tasks, same prompt skeleton, temperature 0. The only
variable is the toolset — Arm A gets real ripgrep + read_file + list_files;
Arm B gets max-context's tools over real stdio MCP. Retrieval tasks graded
objectively (file-set F1 vs a hand-verified key built from an *independent*
oracle — never max-context's own output). Edit tasks graded by a **different**
model (no self-judging) against a pre-registered rubric. Infra failures
(rate-limit, truncation, API errors) are excluded from quality stats and shown
separately — never scored as capability losses.

**Scope (be honest):** 1 repo (httpx @ `b5addb64`), 8 tasks, single replicate
per run. Small n. These are **direction + existence + effect-size** results on a
defined sample, **not** a universal verdict. Tasks/keys/rubric are pre-registered
(hash in each run's manifest). Full transcripts in `results/httpx-run-*/`.

---

## TL;DR

1. **The original product claim did not hold at first.** In a real agent loop
   max-context was *not* better than grep, and on two axes it was worse: it used
   **more tokens**, and it **failed every open-ended edit task by looping** — it
   kept searching instead of answering.
2. **Both problems were fixed** by changing the *tools*, not the model. After the
   fixes, max-context **completed all edit tasks and tied grep on every one**, and
   token cost fell to near-parity.
3. **"Tie → better" attempt (run 6):** we added relationship tasks (list-all-callers,
   blast-radius) — grep's structural blind spot — and fixed the retrieval grader.
   Result: **still a tie on quality** (Cliff's δ=0.00, every per-task Δ=0.00), with
   max-context **slightly cheaper** (median −1,165 tokens/task). On the relationship
   tasks a *capable* grep agent (Sonnet) matched recall; max-context's token savings
   were **real but inconsistent** (+79%, +25%, −103%). **Not yet "better" — a
   clean, reproduced tie.** Beating grep likely needs harder targets and bigger n.
4. **Root-caused the inconsistency → fixed inherited-method resolution (run 7).**
   The −103% loss on C01 ("all callers of build_request") was NOT model behavior:
   `get_call_chain` returned **empty** because `self.build_request()` calls inside
   `Client.request` were `unresolved` — `build_request` is defined on the *parent*
   `BaseClient`, and the resolver didn't walk inheritance. So the model asked the
   right tool, got nothing, and was forced to loop. **Fix:** capture class bases
   (migrationV5 `class_bases`) and walk the inheritance chain in `methodOnType`.
   Result on C01: **16 tool calls → 2, 51,637 tokens → 5,101 (10× cheaper), 47s →
   11s (4× faster)** — same correct answer. This is a correctness fix (the call
   graph was blind to inherited calls) that *also* removed the behavioral loop.

---

## What we found (runs 1–2): the claim failed

| Signal | Result |
|---|---|
| Retrieval quality | Tied / within noise (not significant) |
| Tokens per task | **grep ~1.8× cheaper** (e.g. R01: grep 3,012 vs mc 16,910) |
| Edit tasks (E01, E02, G01) | **max-context looped and never answered** — `truncated_turns` on all of them; grep answered all correctly |

**Root cause (from transcripts):** max-context returned verbose, ranked result
lists. The model read those as "narrow it down, search again" and called
`query_codebase` 20–28 times without committing. grep's terse `path:line:text`
gave a clear "found it, stop" signal that max-context lacked. This reproduced at
both a 1,500- and a 10,000-token output budget, so it was **behavioral, not a
budget artifact**.

**Headline contradiction:** `BENCHMARK.md`'s "~30× fewer tokens" measures a
*single tool call*. Across a full agent session the picture inverted — grep used
fewer tokens. Per-call ≠ per-session.

---

## What we changed (the fixes)

All in the tool layer; the model and the moat (no LLM at index time) are untouched.

1. **New `get_definition` tool** — exact-name lookup that returns one decisive
   `file:line` plus an `answer` field ("definitive location — answer now"). Gives
   the model grep's terminal signal with index precision.
2. **Terser `query_codebase`** — default results 5→3, snippets capped, enrichment
   dropped; when one result is an exact-name match it leads with that result alone.
3. **`get_impact` steering** — guidance now says "for what-depends-on-X use
   get_impact/get_call_chain, don't re-search," plus a **server-side nudge**: after
   several `query_codebase` calls the tool injects "switch tools or commit."

(This intentionally lifted the old 4-tool cap — now 5 tools.)

---

## What we measured after the fixes (runs 3–5): the loop is gone

**Edit-task outcome across all five runs** (mc arm; grep completed all throughout):

| Edit task | Run 1 | Run 2 | Run 3 | Run 4 | Run 5 |
|---|---|---|---|---|---|
| E01 (localized "add a header") | loop | loop | **5/5** | **5/5** | **5/5** |
| E02 (diffuse "what depends on timeout") | loop | loop | loop | **5/5** | **5/5** |
| G01 (planted grep-win, "why trust_env") | — | loop | loop | **3/3** | **3/3** |

In **runs 4 and 5**, max-context completed **all three** edit tasks and **tied
grep on every one** (5/5, 5/5, 3/3) — including the task planted to favor grep.
The query-loop, its worst and most reproducible failure across runs 1–3, is
eliminated and the fix now **reproduces across two independent runs**.

**Tokens equalized.** Median per-task token delta (mc − grep):

| | Run 1 | Run 4 | Run 5 |
|---|---|---|---|
| median Δ tokens | **+5,377** (grep much cheaper) | **+634** | **+659** (near parity) |

On the diffuse G01 task, max-context used roughly **half** grep's tokens in both
runs 4–5 (e.g. run 4: 51k vs 111k). When a task genuinely spans many files, the
index now wins on cost.

---

## The remaining gap is now mostly a *grading* artifact

**Retrieval precision on overloaded names.** mc retrieval F1 is still <1.0 on
overloaded symbols (run 5: R01 `URL` 0.50, R04 `Timeout` 0.67, R05 0.67). But the
run-5 transcripts show **the tool fix is working and the model's answer is
correct** — the score is lost to how we grade:

> R01 transcript: `get_definition` returned `canonical: URL → httpx/_urls.py`.
> The model answered: *"The main `URL` class is defined in **httpx/_urls.py**.
> The other matches (`url` in _models.py, conftest.py) are just methods that
> return a URL, not the class definition."* — **correct.**

The objective grader (`ExtractFiles`) regex-scrapes *every* file path in the
answer, so files the model named only to **dismiss** them count against
precision. So the residual gap is substantially a **measurement problem**, not a
max-context problem: the grader can't distinguish "the answer is X" from "X, and
explicitly not Y or Z."

**Fix shipped (tool side):** `get_definition`/`query_codebase` now return
`answer_status`, `recommended_next_action`, `canonical`, and secondary-match
metadata; for overloaded names the **type/class definition is ranked first** (what
"where is X defined?" almost always means). The model uses it correctly.

**Fix still needed (grader side):** score retrieval against the *canonical/lead*
file the answer endorses, not every path mentioned — e.g. weight the first-named
or explicitly-affirmed file, or have the grader read the `canonical` the model
was given. Until then, treat these retrieval F1s as a floor, not the true value.

---

## Honest caveats

- **n is small:** 8 tasks, 1 repo, 1 replicate per run. Edit-task results even
  flip between runs (E01 looped in run 2, completed in run 3). Treat the
  trajectory as a strong direction, not a settled quality number.
- **Fixes shipped together,** so run 4 shows their combined effect, not
  per-change attribution.
- **One model** (Claude Sonnet 4.6). The loop behavior is a model×tool-design
  interaction; another model may differ.
- Quality deltas are still not statistically significant (Wilcoxon p≈0.11–0.32).
  The defensible claims are the *qualitative* ones: the edit loop is fixed, and
  token cost moved from grep-favored to parity.

---

## How the harness kept itself honest

Three real confounds were caught and fixed mid-experiment — each would have
faked a max-context "win" if undetected:

- **`rg` was a shell function, not a binary** → the first grep arm silently
  errored on every call. Fixed with an explicit `--rg` path + a startup preflight
  that aborts if grep can't execute.
- **Rate-limit death / budget runaway** → added per-task checkpointing (no lost
  work), exact `Retry-After` honoring, and a `budget_halt` guardrail.
- **Judge model silently invalid on Bedrock** → surfaced as a recorded
  `gradeerr`, then fixed with explicit `--judge-model`. (Bedrock model ids are
  inconsistent: sonnet works unversioned, haiku needs the version suffix.)

Every run ends with an explicit status; nothing is dropped silently.

---

## Backends

Same agent loop, pluggable `Caller`:
- **Anthropic API** — `--backend anthropic`, needs `ANTHROPIC_API_KEY`.
- **AWS Bedrock** — `--backend bedrock --aws-profile <p>`, shells out to
  `aws bedrock-runtime converse`; set full ids with `--task-model`/`--judge-model`.
  Higher throughput made runs ~3× faster than the rate-limited API key.

---

## Reproduce

See `README.md`. Each `results/httpx-run-{1..5}/` holds the machine-readable
`results.json` (manifest + per-run records), `report.md`, and full per-run
transcripts. A third party can re-grade with their own judge or re-run with their
own prompts. The prior incremental notes are kept in
`FINDINGS-archive-incremental.md`.

## Next steps

1. Re-run the httpx retrieval tasks to confirm the overloaded-name precision
   regression is closed.
2. Scale to flask / got / zod and add replicates for cross-repo n (cheap now via
   Bedrock).
3. Add per-fix ablations for the tool-layer changes.
