# Does an AI assistant actually do better with max-context than with grep?

*A public-facing summary of the causal A/B evaluation. Full methodology, per-task
data, transcripts, and pre-registration hashes are in [`FINDINGS.md`](./FINDINGS.md)
and `results/`.*

## The question

The token benchmark ([`BENCHMARK.md`](../../BENCHMARK.md)) shows max-context returns
~30× fewer tokens per query than a skilled grep. But fewer tokens per *call* doesn't
prove an agent produces better *outcomes*. So we ran a causal experiment:

> Same model. Same tasks. Same turn budget. **Only the tools differ** — one arm gets
> real `ripgrep` + `read_file`, the other gets max-context over MCP. An independent,
> different-model judge grades the answers blind. Tasks and grading are pre-registered.

## What we found

**1. Equal answer quality — a tie, not a win (and we tested hard for a win).**
Across 13 tasks on a real codebase (httpx), max-context and a skilled grep agent scored
the *same* on answer quality. We even built tasks designed to break grep's recall (deep
5-hop call chains); a capable grep agent, told to trace level-by-level, kept up. **We
report this honestly: max-context is as accurate, not more accurate** — on
naturally-occurring tasks.

**2. Materially cheaper — most on "what-calls-this" work.** Same correct answer, far
fewer tokens:

| Task (find all callers of…) | grep tokens | max-context tokens | saving |
|---|---|---|---|
| build_request | 76,138 | 5,076 | −93% |
| transitive callers, 5-hop chain | 132,093 | 7,898 | −94% |
| transitive callers, 4-hop chain | 105,364 | 7,468 | −93% |

grep can *be* exhaustive, but it pays for it linearly in searches and file reads; the
call graph answers in ~2 tool calls. Summed over all relationship tasks: **~90% fewer
tokens** for the same answer.

**3. One structural quality win: aliased imports.** When a function is imported under a
different name (`from m import handler as h`, then `h()`), grep searching the original
name **can't find the call site** — the text doesn't match. In a controlled test:

| | grep | max-context |
|---|---|---|
| callers of an aliased function found | **0 of 5** | **5 of 5** |

This is the one place max-context is *strictly more accurate*, because it follows
imports instead of matching text. It's real — but **rare**: a survey of 16 popular
repos found internal hot-function aliasing is uncommon, so we frame it as "catches a
class of references text search misses," not "categorically smarter."

## What we do NOT claim

- **Not "smarter answers" in general.** Quality is a tie except the alias case.
- **Not a universal law.** Small sample: 1 repo family, 1 replicate. These are
  direction + effect-size results on defined tasks at pinned commits.
- **Not faster reasoning.** The model does the thinking; max-context gives it less to
  read and references text search can't resolve.

## How we kept it honest

- **Non-strawman grep arm** — real ripgrep, full flag surface, coached to trace call
  chains manually.
- **Non-circular ground truth** — answer keys built independently (ripgrep + manual),
  never from max-context's own output.
- **Blind, different-model judge** for open-ended tasks (no self-grading).
- **Pre-registered** tasks + rubric, hashed before running; all transcripts published.
- **No silent drops** — every run records a status; infra failures are reported, not
  hidden. (One such failure — a transient index-rebuild race — was found, diagnosed
  from the transcript, and fixed at the root.)

## The bottom line for the website

> **max-context matches a skilled grep agent's answer quality and costs far less to get
> there — up to 94% fewer tokens (≈90% in aggregate) on call-graph questions — and resolves references like
> aliased imports that text search structurally can't.** Equal answers, materially
> cheaper, with a correctness edge on the cases grep is blind to.

*Reproduce: see [`FINDINGS.md`](./FINDINGS.md) and `results/` (per-run records, judge
transcripts, protocol hashes `b5a83409…` and `249ecc5e…`).*
