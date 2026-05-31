# LoCoBench benchmark arm

**STATUS:** 🟡 wired — pipeline verified (loads scenarios, materializes + indexes
the real synthetic project, runs both arms, grades with the blind judge). Live
smoke test in progress; no headline numbers yet.

## Decisions locked in

- **Option A (repurpose):** we do NOT inject LoCoBench's full-context blob. Each
  scenario's synthetic project is indexed; both arms get only the task and gather
  context themselves (max-context MCP vs grep+read).
- **Discovery prompt mode (default):** LoCoBench's `task_prompt` pre-names the files
  to edit. We strip that step-by-step recipe and pass only title + description + the
  ask, forcing each arm to locate the code — the cleanest test of navigation value.
  `--intent full` replays the verbatim prompt instead.
- **Blind-judge grading:** the shared different-model rubric judge scores the answer
  against the scenario's shipped `ground_truth` (reference) + `evaluation_criteria`
  (one binary point each). Identical per arm → never biases the mc-vs-grep delta.
  (LoCoBench's native compile+test LCBS validator is deferred — needs Python + 10
  toolchains.)

## Data

`data/` holds the extracted LoCoBench dataset (gitignored, 249MB zip → ~600MB):
`data/generated/<project>/<InnerName>/` (real source) + `data/output/scenarios/*.json`
(8,000 tasks). Fetch: `gdown 1pK1M1sRrVZUDMKYcwh49CdXug0UzStvl` (or curl via the
Drive confirm-token), `unzip`, then flatten so `data/generated` + `data/output` sit
directly under `data/`.

## Run

```bash
MC=../../../bin/max-context   # build with `make build` at repo root first
# Dry-run: load + index, no LLM calls
go run ./cmd/eval --data data --lang go --category feature_implementation --limit 1 \
  --mc-bin "$MC" --rg "$(command -v rg)" --dry-run

# Live (Bedrock): one paired run
go run ./cmd/eval --data data --lang go --category feature_implementation --limit 1 \
  --mc-bin "$MC" --rg "$HOME/.local/bin/rg" \
  --backend bedrock --aws-region us-east-1 --aws-profile <profile> \
  --task-model us.anthropic.claude-sonnet-4-6 \
  --judge-model us.anthropic.claude-haiku-4-5-20251001-v1:0
```

Filters: `--lang` (go,typescript,…), `--category`, `--difficulty`, `--limit`,
`--id-list <file>` (run an exact scenario set, e.g. `fair-fight-subset.txt`).
`--arms grep,max-context,hybrid` selects which arms run, paired per scenario:
- **grep** — baseline (ripgrep + read_file + list_files)
- **max-context** — mc-only mechanism probe (the 5 MCP tools, no file reading)
- **hybrid** — REALISTIC deployment: grep+read+list AND the 5 MCP tools (what a
  host looks like with max-context installed; MCP tools are additive)

Note: `rg` must be a real binary, not a shell function — pass `--rg` explicitly.

## Supporting tools

- `go run ./cmd/coverage --data data [--list-fully-indexable out.txt]` — static,
  no-LLM analysis of which scenarios max-context can index (the coverage gap).
- `go run ./cmd/pool results-*/results.json …` — pools runs across batches into a
  paired analysis (quality %, token ratio, sign test) over scenarios where the
  compared arms both completed (truncations excluded from quality, not zeroed).

## Findings

See `FINDINGS.md` (results + caveats) and `PRODUCT-NOTES.md` (positioning). Headline:
the coverage gap (only ~10% of LoCoBench is fully indexable) is the solid result;
on a fair-oracle closed-ended subset, mc-only led quality at lower cost, and —
surprisingly — hybrid underperformed mc-only. All directional pending larger n.

---

### Original plan notes

**Source:** LoCoBench: A Benchmark for Long-Context Large Language Models in
Complex Software Engineering — arXiv [2509.09614](https://arxiv.org/abs/2509.09614)
(Sep 2025).

## Why this one first

It isolates **retrieval / understanding** rather than downstream edit success,
so a result is unambiguous (a SWE-bench loss could be the agent, not our
retrieval — a LoCoBench result is about the context fed in). It is **multilingual
(10 languages)**, which finally exercises the Go/TS/Java engine instead of
httpx-Python only — directly attacking the "1 repo family" objection in
`../in-house/FINDINGS.md`.

## Axis mapping (LoCoBench task category → our tool)

| LoCoBench category | max-context tool / mechanism | Our axis |
|--------------------|------------------------------|----------|
| architectural understanding | `get_architecture` | retrieval |
| cross-file refactoring | v3 cross-file import resolution | recall |
| bug investigation | `get_call_chain` / `get_impact` | edit-localization |
| code comprehension | `query_codebase` / `get_definition` | retrieval |

Categories we will likely **exclude** (signal blurs): the 1M-token raw-context
comprehension scenarios test the *model's* context window, not our retrieval —
keep to the cross-file / investigation tasks where retrieval is the bottleneck.

## Open questions before wiring

- [ ] Fetch task schema; map to `internal/spec` task format from in-house.
- [ ] Decide: trust LoCoBench's own LCBS judge, or re-grade with our
      independent-oracle approach (in-house used the latter; keep consistent).
- [ ] Pick the language subset to run first (Go + TS to prove multilingual).

## Layout (planned)

```
locobench/
├── go.mod          (own module, harness copied from in-house)
├── cmd/            eval + report runners
├── internal/       agent / arms / grade / spec / stats (copied, then adapted)
├── tasks/          LoCoBench-derived task specs
├── results/        per-run results.json + transcripts
└── FINDINGS.md     written after first run
```
