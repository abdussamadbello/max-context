# Benchmark

max-context vs naive and skilled `Grep`+`Read` baselines, measured in tokens consumed by the LLM.

## TL;DR

| Repo | Questions | max-context (avg) | Naive (avg) | Skilled (avg) | vs Naive | vs Skilled |
|---|---|---|---|---|---|---|
| max-context (self) | 20 | 229 | 38,929,834 | 6,774 | 169,776.9× | 29.5× |

Phase 8.1 will extend this benchmark with `cli/cli` and `vitejs/vite`. The self-repo numbers above are reproducible today via `max-context bench` (see Reproduce).

## Methodology

- **We measure** cl100k_base tokens consumed by what the LLM would see — tool-call JSON for max-context; `grep` output plus file/window contents for the baselines.
- **We do not measure** latency, correctness, or LLM answer quality. Different agents will perform differently; this is a *context-budget* measurement, not an *intelligence* measurement.
- **Baselines run a deterministic Go script**, not a real LLM agent. A real agent may do better (smarter grep flags) or worse (re-reading files).
- **Naive baseline**: recursive `grep` across all files (no directory exclusions), full `os.ReadFile` of every matching file, no dedup across query terms. This tokenizes everything the file walker can see, *including build artifacts and the indexed binary itself*, which inflates the number — that is precisely what an unfiltered agent would consume.
- **Skilled baseline**: `grep` with `node_modules`/`.git`/`vendor`/`bin`/`.max-context` excluded, `Read` only ±20 lines around each match, dedupe windows within 40 lines of each other.
- **Where max-context loses**: semantic questions ("why was this written this way?") that require reading prose, not symbols.

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

- **Phase 8.1**: Extend benchmark to `cli/cli` (~100K LOC Go) and `vitejs/vite` (~80K LOC TypeScript) for cross-language evidence.
- Python and Rust benchmark repos in a later phase.
