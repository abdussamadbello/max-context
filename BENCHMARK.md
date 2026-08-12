# Benchmark

max-context vs naive and skilled `Grep`+`Read` baselines, measured in tokens consumed by the LLM for a single deterministic tool answer.

This benchmark is a **per-tool-call context-budget benchmark**, not a full agent-session benchmark. The causal A/B evaluation in `experiments/eval/benchmarks/in-house/FINDINGS.md` found that per-call savings do not automatically translate into end-to-end agent savings: verbose or ambiguous tool responses can make a model keep searching. The MCP tools now return explicit `answer_status` / `recommended_next_action` fields and canonical overloaded-symbol results to reduce that failure mode, but session-level cost should be measured separately.

## TL;DR

| Repo | Questions | max-context per call (avg) | Naive per call (avg) | Skilled per call (avg) | vs Naive | vs Skilled |
|---|---|---|---|---|---|---|
| max-context (self) | 20 | 229 *(asserted — see below)* | 41,344 | 13,122 | 180.3× | 57.2× |

> [!WARNING]
> **The max-context column is not measured, so the ratios above are an upper
> bound.** `mc_response_tokens` is a hand-written integer per question in
> `benchmark/questions/max-context.json`; only the two baselines are computed by
> actually running grep+read. The ratio therefore moves when the repo grows and
> never when the tool's output changes.
>
> Measuring all 20 questions against the current binary (each call using the
> direction its question implies) gives **611 tokens per call, 2.67× the
> asserted figure** — which puts the honest result at **67.6× vs naive and
> 21.5× vs skilled**.
>
> The gap is concentrated in `get_call_chain`: asserted at 49–134 tokens,
> it actually returns 371–3,272. "What calls `Open` in the db package?" is
> recorded as 49 tokens and really costs 3,272, because `Open` has wide fan-in
> and nothing caps the traversal. `get_impact` is accurate to within ~10%, and
> `query_codebase` is *cheaper* than claimed on 4 of 7 questions since the
> definitive path returns one canonical result instead of a ranked list.
>
> Until the harness measures the tool the way it measures the baselines, quote
> 21.5× rather than 57.2×.

> **These numbers replace an earlier, much larger set (169,776.9× vs naive).** That
> figure was an artefact of the harness, not a result: the naive baseline walked
> `bin/` and tokenized the project's own compiled binary, and both baselines
> grepped files — 24MB of recorded eval transcripts under `experiments/` — that
> max-context never indexed. Both are fixed (see Methodology); treat any
> previously published naive figure as withdrawn.

The self-repo baseline numbers above are reproducible today via `max-context bench` (see Reproduce); the max-context column is not, for the reason stated above. Treat them as a token-volume result for the tool response itself. For task completion quality, repeated tool calls, and agent-session token cost, use the A/B harness under `experiments/eval/`.

## Methodology

- **We measure** cl100k_base tokens consumed by what the LLM would see for one deterministic answer: tool-call JSON for max-context; `grep` output plus file/window contents for the baselines.
- **We do not measure** latency, correctness, or LLM answer quality. Different agents will perform differently; this is a *context-budget* measurement, not an *intelligence* measurement.
- **Baselines run a deterministic Go script**, not a real LLM agent. A real agent may do better (smarter grep flags) or worse (re-reading files).
- **Same file set on both sides.** The baselines walk exactly what max-context indexes — the repo's `.gitignore` / `.contextignore` rules — so the comparison measures *how* each approach searches, not *how much* it was pointed at. Before this, grep paid for files the index had never seen, which flattered max-context by roughly 40×.
- **Binary files are skipped**, using grep's own heuristic (a NUL byte in the first block). Tokenizing a compiled binary is not something any agent does; counting it made the naive baseline meaningless rather than merely unflattering.
- **Naive baseline**: recursive `grep` across all in-scope files (no directory exclusions beyond the repo's ignore rules), full `os.ReadFile` of every matching file, no dedup across query terms — what an agent that greps and reads whole files consumes.
- **Skilled baseline**: `grep` with `node_modules`/`.git`/`vendor`/`bin`/`.max-context` additionally excluded, `Read` only ±20 lines around each match, dedupe windows within 40 lines of each other.
- **The max-context side is asserted, not measured** — the known gap above. Fixing it means invoking each question's tool and tokenizing the response, exactly as the baselines are computed, so `mc_response_tokens` becomes an output of the run rather than an input to it.
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
