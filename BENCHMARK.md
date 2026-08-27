# Benchmark

max-context vs naive and skilled `Grep`+`Read` baselines, measured in tokens consumed by the LLM for a single deterministic tool answer.

This benchmark is a **per-tool-call context-budget benchmark**, not a full agent-session benchmark. The causal A/B evaluation in `experiments/eval/benchmarks/in-house/FINDINGS.md` found that per-call savings do not automatically translate into end-to-end agent savings: verbose or ambiguous tool responses can make a model keep searching. The MCP tools now return explicit `answer_status` / `recommended_next_action` fields and canonical overloaded-symbol results to reduce that failure mode, but session-level cost should be measured separately.

## TL;DR

| Repo | Questions | max-context per call (avg) | Naive per call (avg) | Skilled per call (avg) | vs Naive | vs Skilled |
|---|---|---|---|---|---|---|
| max-context (self) | 20 | 1,214 | 43,158 | 13,671 | 35.5× | 11.3× |

> **Both sides are now measured.** An earlier version of this table carried a
> hand-written `mc_response_tokens` per question while only the baselines were
> computed, so the ratio moved when the repo grew and never when the tool output
> changed. The runner now invokes each question's tool against the real index and
> counts the response, the same way it runs real greps for the baselines, and
> refuses to run without a way to do so. Previously published figures — 169,776.9×,
> 173.9×, 180.3× vs naive; 29.5×, 55.2×, 57.2× vs skilled — are all withdrawn.
>
> The drop from 57.2× to 11.3× is the correction, not a regression: it is what
> the tools always cost, now that they are being asked instead of assumed.

Every number above is reproducible today via `max-context bench` (see Reproduce). Treat them as a token-volume result for the tool response itself. For task completion quality, repeated tool calls, and agent-session token cost, use the A/B harness under `experiments/eval/`.

## Methodology

- **We measure** cl100k_base tokens consumed by what the LLM would see for one deterministic answer: tool-call JSON for max-context; `grep` output plus file/window contents for the baselines.
- **We do not measure** latency, correctness, or LLM answer quality. Different agents will perform differently; this is a *context-budget* measurement, not an *intelligence* measurement.
- **Baselines run a deterministic Go script**, not a real LLM agent. A real agent may do better (smarter grep flags) or worse (re-reading files).
- **Same file set on both sides.** The baselines walk exactly what max-context indexes — the repo's `.gitignore` / `.contextignore` rules — so the comparison measures *how* each approach searches, not *how much* it was pointed at. Before this, grep paid for files the index had never seen, which flattered max-context by roughly 40×.
- **Binary files are skipped**, using grep's own heuristic (a NUL byte in the first block). Tokenizing a compiled binary is not something any agent does; counting it made the naive baseline meaningless rather than merely unflattering.
- **Naive baseline**: recursive `grep` across all in-scope files (no directory exclusions beyond the repo's ignore rules), full `os.ReadFile` of every matching file, no dedup across query terms — what an agent that greps and reads whole files consumes.
- **Skilled baseline**: `grep` with `node_modules`/`.git`/`vendor`/`bin`/`.max-context` additionally excluded, `Read` only ±20 lines around each match, dedupe windows within 40 lines of each other.
- **Both sides are measured the same way.** Each question records the tool and the exact `mc_args` it is invoked with; the runner calls the tool against the real index and tokenizes what comes back. An empty response or a tool error fails the run rather than scoring as free, so a question that goes stale against the index is loud instead of flattering.
- **Where the cost sits.** Lookups (`query_codebase`, `get_definition`) run 213–414 tokens. `get_call_chain` runs 371–1,778, capped at 50 results per direction with the true total reported. Historical uncapped `get_impact` responses ran 724–4,565 tokens. It now accepts `token_budget`, ranks discovered nodes by graph depth and edge confidence, and reports exact budget omissions; omitting the argument preserves the historical response for compatibility.
- **Where max-context loses**: semantic questions ("why was this written this way?") that require reading prose, not symbols.

## Agent-session metric

The session-level question is: how many turns, tool calls, and total tokens does an agent spend before producing the correct final answer?

Track these separately from this benchmark:

- total input/output tokens per task
- total tool calls per task
- repeated same-tool/same-query calls
- turns to final answer
- retrieval precision/recall or edit rubric score

The current A/B findings (`experiments/eval/benchmarks/in-house/FINDINGS.md`) show the important distinction: early max-context responses were cheap per call but expensive per session because the model looped; after terser responses, `get_definition`, impact steering, loop guards, and canonical overloaded-symbol ranking, the loop behavior is explicitly guarded against. The published runs find **equal answer quality to a skilled grep agent** on the tasks measured, at **up to 94% fewer tokens (≈90% in aggregate)** on call-graph / "what-calls-this" questions, plus a strict recall win on **aliased imports** (a controlled task where grep found 0 of 5 callers and max-context found all 5). Honest scope: small sample (1 repo family, 1 replicate); quality is a tie, not a broad win.

## Reproduce

```bash
git clone https://github.com/maxcontext/max-context
cd max-context && make build
./bin/max-context --index
./bin/max-context bench -repo . -questions benchmark/questions/max-context.json -out benchmark/runs/max-context
```

## Per-repo results

See `benchmark/runs/max-context/results.json` and `benchmark/runs/max-context/benchmark.md` for full per-question breakdowns.

## Cross-repo: does it hold outside our own codebase?

Every figure above is n=1 on max-context's own repo, the most self-flattering
sample available. Run against three outside projects in three languages,
indexed and measured identically:

| Repo | Questions | Lookup | Trace | Impact | Blended |
|---|---|---|---|---|---|
| max-context (Go, self) | 20 | 32.1× | 15.5× | 6.5× | 11.3× |
| cobra (Go) | 11 | 41.7× | 31.8× | 6.5× | 14.4× |
| flask (Python) | 11 | 47.4× | 36.9× | 32.7× | 39.0× |
| zod (TypeScript) | 11 | 43.9× | 8.3× | 148.2× | 93.7× |

**The lookup result is the robust one.** "Where is X defined?" costs
289–468 tokens regardless of repo, against a grep baseline that grows with the
codebase — **32–47× across four repos and three languages, a spread of only
1.5×**. That is the claim worth making.

**The blended number is not meaningful, and neither is the impact column.**
Both are dominated by how greppable the symbols in a question happen to be,
which is a property of the repo's naming, not of max-context:

- zod's `I03` greps for `unknown`, `string`, `_enum` — real symbols in the file
  under test, but `string` in a TypeScript repo matches almost everything. That
  one question costs the baseline 897,890 tokens and scores 798×, dragging
  zod's impact average from roughly 4× to 148× and its blended figure to 93.7×.
- The first version of these question sets derived grep terms from file
  *basenames*, producing `app` for flask and `api` for zod. Those runs reported
  129× and 113×. They are not in the table because the terms were strawmen.

So: quote the lookup number, report per category, and treat any single blended
figure — including the one at the top of this file — as an artefact of question
mix. A benchmark whose headline moves 200× on one term choice is measuring the
question set at least as much as the tool.

The harness now refuses a question whose baseline terms match nothing, which is
how cobra's original `flag_groups` question was caught scoring 0×.

## Roadmap

- Extend both per-call and agent-session benchmarks to `flask`, `got`, `zod`, `cli/cli`, and `vitejs/vite` for cross-language evidence.
- Add repeated runs and per-fix ablations for `get_definition`, terse `query_codebase`, impact steering, loop guards, and canonical overloaded-symbol ranking.
