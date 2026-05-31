# max-context evals

Each subfolder under `benchmarks/` is a **self-contained benchmark** — its own
Go module, tasks, results, and findings. The shared two-arm design (max-context
MCP vs grep+read, same model, same budget, blind different-model judge) is
copied per benchmark rather than shared, so each can evolve independently.

## Benchmarks

| Folder | Source | Year | Status | What it stresses |
|--------|--------|------|--------|------------------|
| [`in-house/`](benchmarks/in-house/) | httpx + constructed repos | 2024–25 | ✅ done (13 tasks + alias) | retrieval F1, caller recall, edit-localization, token cost |
| [`locobench/`](benchmarks/locobench/) | LoCoBench (arXiv 2509.09614) | 2025 | 🔜 next | long-context: architecture, cross-file refactor, bug investigation — 10 langs |
| [`swebench-pro/`](benchmarks/swebench-pro/) | SWE-bench Pro (arXiv 2509.16941) | 2025 | ⬜ planned | long-horizon agentic edits, 41 repos, multi-file |
| [`coreqa/`](benchmarks/coreqa/) | CoReQA | 2025 | ⬜ planned | repo-level NL question answering (retrieval axis) |

## Run order (cheapest-signal-first)

1. **LoCoBench** — isolates retrieval/understanding (unambiguous result), multilingual.
2. **CoReQA** — repo QA, retrieval axis, cheap.
3. **SWE-bench Pro** — expensive agentic-edit headline; run only if 1–2 look promising.

See each folder's `README.md` for the per-benchmark plan and axis mapping.
