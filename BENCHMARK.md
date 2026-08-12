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
- **Where the cost sits.** Lookups (`query_codebase`, `get_definition`) run 213–414 tokens. `get_call_chain` runs 371–1,778, capped at 50 results per direction with the true total reported. `get_impact` is now the most expensive at 724–4,565 tokens: its cap is `maxImpactNodes = 1000`, a runaway guard rather than a context budget, so a change to a widely-depended-on file returns everything. That is the next thing worth tuning.
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

## Roadmap

- Extend both per-call and agent-session benchmarks to `flask`, `got`, `zod`, `cli/cli`, and `vitejs/vite` for cross-language evidence.
- Add repeated runs and per-fix ablations for `get_definition`, terse `query_codebase`, impact steering, loop guards, and canonical overloaded-symbol ranking.
