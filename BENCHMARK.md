# Benchmark

max-context vs naive and skilled `Grep`+`Read` baselines, measured in tokens consumed by the LLM for a single deterministic tool answer.

This benchmark is a **per-tool-call context-budget benchmark**, not a full agent-session benchmark. The causal A/B evaluation in `experiments/eval/benchmarks/in-house/FINDINGS.md` found that per-call savings do not automatically translate into end-to-end agent savings: verbose or ambiguous tool responses can make a model keep searching. The MCP tools now return explicit `answer_status` / `recommended_next_action` fields and canonical overloaded-symbol results to reduce that failure mode, but session-level cost should be measured separately.

## TL;DR

| Repo | Questions | max-context per call (avg) | Naive per call (avg) | Skilled per call (avg) | vs Naive | vs Skilled |
|---|---|---|---|---|---|---|
| max-context (self) | 20 | 229 | 38,929,834 | 6,774 | 169,776.9× | 29.5× |

The self-repo numbers above are reproducible today via `max-context bench` (see Reproduce). Treat them as a token-volume result for the tool response itself. For task completion quality, repeated tool calls, and agent-session token cost, use the A/B harness under `experiments/eval/`.

## Methodology

- **We measure** cl100k_base tokens consumed by what the LLM would see for one deterministic answer: tool-call JSON for max-context; `grep` output plus file/window contents for the baselines.
- **We do not measure** latency, correctness, or LLM answer quality. Different agents will perform differently; this is a *context-budget* measurement, not an *intelligence* measurement.
- **Baselines run a deterministic Go script**, not a real LLM agent. A real agent may do better (smarter grep flags) or worse (re-reading files).
- **Naive baseline**: recursive `grep` across all files (no directory exclusions), full `os.ReadFile` of every matching file, no dedup across query terms. This tokenizes everything the file walker can see, *including build artifacts and the indexed binary itself*, which inflates the number — that is precisely what an unfiltered agent would consume.
- **Skilled baseline**: `grep` with `node_modules`/`.git`/`vendor`/`bin`/`.max-context` excluded, `Read` only ±20 lines around each match, dedupe windows within 40 lines of each other.
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
./bin/max-context bench --repo . --out benchmark/runs/max-context
```

## Per-repo results

See `benchmark/runs/max-context/results.json` and `benchmark/runs/max-context/benchmark.md` for full per-question breakdowns.

## Roadmap

- Extend both per-call and agent-session benchmarks to `flask`, `got`, `zod`, `cli/cli`, and `vitejs/vite` for cross-language evidence.
- Add repeated runs and per-fix ablations for `get_definition`, terse `query_codebase`, impact steering, loop guards, and canonical overloaded-symbol ranking.
