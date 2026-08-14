# max-context Causal A/B Experiment

Does an LLM **understand and edit codebases better with max-context than with grep**?

The token benchmark (`../../BENCHMARK.md`) shows max-context returns far fewer
tokens for a single deterministic tool answer, and resolver validation proves
its call graph is correct. Neither proves the *causal* claim that an LLM produces
**better outcomes** with it across a full agent session. This harness tests that
claim directly, and honestly reports where it does and doesn't hold.

## Design (why you can trust it)

- **Isolated module.** This lives in its own Go module (`github.com/maxcontext/eval`). The
  shipped `max-context` core stays 100% LLM-free — verify: `cd ../.. && go list -deps ./... | grep -i anthropic` returns nothing.
- **Two arms, one variable.** Same model, prompt skeleton, turn budget, temperature 0. The
  only difference is the toolset:
  - **Arm A (grep):** real `ripgrep` (full flag surface), `read_file` (ranges + whole), `list_files`. It chooses its own search terms — no pre-supplied terms (anti-strawman).
  - **Arm B (max-context):** the real MCP tools over **stdio MCP** to the actual `max-context` binary (the real integration path). Tools are fetched via `tools/list` — no hardcoded schemas.
- **Objective-first grading.** Retrieval tasks → file-set **precision/recall/F1** vs a
  hand-verified key built from an **independent** oracle (ripgrep / LSP), never from
  max-context output (no circularity). Edit tasks → a **blind, different-model** LLM judge
  against a **pre-registered atomic rubric** + reference answer.
- **Pre-registration.** The protocol (tasks, keys, rubric, models) hashes to a fixed SHA-256.
  Commit it *before* running; recompute after to prove nothing was tuned to results.
- **Planted grep-wins.** Tasks chosen to favor grep (prose/"why"/dynamic) are included and
  reported — mapping the boundary of where max-context helps.
- **No silent drops.** Every run ends with an explicit status; failures/truncations appear in
  the report's failure table.
- **Honest stats.** Paired Wilcoxon signed-rank + McNemar, effect sizes, bootstrap CIs. n is
  small by design — the claim is existence + direction + effect size on a defined sample, not a
  universal law.

## The free half: `go run ./cmd/ceiling`

The agent experiment below needs an API key and costs money per run, so it gets
run rarely and its numbers age. The **retrieval ceiling** measures a weaker but
objectively checkable thing with no model in the loop — whether each arm's tools
surface the answer at all — so it costs nothing and runs on every push in CI.

```bash
go run ./cmd/ceiling            # committed fixtures only, no network
go run ./cmd/ceiling -remote    # adds probes that clone a real upstream repo
```

It shares the hand-verified gold sets with the agent protocol, and a test fails
if the two drift. See [CEILING.md](CEILING.md) for results — including a
correction it turned up in this project's own A02 answer-key note.

The two are complements, not substitutes: a ceiling bounds what any model could
find; only the agent run below shows what a model actually does with it.

## Run the agent experiment

Prereqs: `go`, `git`, `ripgrep`, a built `max-context` binary, and `ANTHROPIC_API_KEY`.

```bash
# 1. Build the max-context binary the experiment drives over MCP:
( cd ../.. && go build -o /tmp/max-context ./cmd/max-context )

# 2. Validate everything WITHOUT spending on the API (clone + index + wire arms):
go run ./cmd/eval --protocol protocol/httpx.json --mc-bin /tmp/max-context --dry-run

# 3. Run the live experiment (needs ANTHROPIC_API_KEY):
export ANTHROPIC_API_KEY=sk-...
go run ./cmd/eval --protocol protocol/httpx.json --mc-bin /tmp/max-context --out results

# 4. Generate the report:
go run ./cmd/report --results results/results.json --protocol protocol/httpx.json --out results/report.md
```

Outputs: `results/results.json` (machine-readable, with manifest + per-run records),
`results/transcripts/` (full request/response traces for audit), `results/report.md`.

## Reproduce / audit

The manifest records pinned repo SHAs, the index SHA (asserted == checkout), model ids,
rg version, both arm playbooks verbatim, and the protocol hash. A third party can re-grade
with their own judge (re-run `cmd/report` after editing the judge) or re-run with their own
prompts (edit `protocol/*.json` — the hash will change, exposing the edit).

## Status

Harness complete and tested offline (agent loop, both arms incl. real-binary MCP integration,
grading, stats, report). The **live numbers require `ANTHROPIC_API_KEY`** and have not yet
been produced. `protocol/httpx.json` is the vertical-slice protocol; additional repos
(flask, got, zod) extend it with the same schema.
