# Retrieval ceiling — a no-LLM A/B you can run for free

```
cd experiments/eval/benchmarks/in-house
go run ./cmd/ceiling            # local fixtures only, no network
go run ./cmd/ceiling -remote    # adds probes that clone a real upstream repo
```

No API key, no Bedrock, no model, no cost. It builds `max-context` from the
parent repo, indexes each probe repo, runs both arms' tool calls, and scores
what came back against the same hand-verified gold sets the agent experiment
uses.

## What this measures — and what it does not

It measures the **ceiling**: whether an arm's tools put the answer in front of a
model at all. A caller that appears in no tool response cannot be named by any
model, however good. That bound is objectively checkable without an LLM.

It does **not** measure whether an agent actually finds the answer. A model can
be handed the right output and still miss it. For that, `cmd/eval` runs the real
agent loop — and that one costs money. The two are complements: this is the
cheap upper bound, that is the real measurement.

## Results (`ceiling-v1`, run 2026-08-13)

### A01 — `payments`, controlled alias fixture

`post_transaction` is defined in `ledger.py`, imported as `apply_entry` in
`billing.py` and as `record_payment` in `payroll.py`. Five functions call it;
none of them spell its name.

| Arm | Recall | Tool calls | Output bytes |
|---|---|---|---|
| grep (one-shot) | **0/5** | 4 | 518 |
| grep (alias-chained) | **5/5** | 6 | 886 |
| max-context | **5/5** | 1 | 660 |

Four different searches for the real name — bare, word-boundary,
call-shaped, definition-anchored — reach the definition and the two import
lines, and not one of the five call sites. `get_call_chain` returns all five in
one call, each tagged `resolution=import-symbol`.

**But a skilled engineer does not stop at the first result.** Following the
aliases that the first search revealed also finds all five. So the honest
finding is not "grep cannot do this" — it is that grep needs 6 searches and a
correct inference about aliasing where max-context needs 1 call and none. That
is a real difference in an agent loop, where every search is a round trip, but
it is a cost difference, not an impossibility. The one-shot number alone would
be a strawman, which is why both tiers are reported.

### A02 — `celery@90e2a13`, real upstream repo

| Arm | Recall | Tool calls | Output bytes |
|---|---|---|---|
| grep (one-shot) | 1/1 | 4 | 784 |
| grep (alias-chained) | 1/1 | 5 | 877 |
| max-context | 1/1 | 1 | 390 |

**This run corrects a claim in this project's own answer key.** The A02 note in
`protocol/alias-v4.json` says a search for `resolve_all` "finds def+import, not
the aliased call site." That is true of the pattern `resolve_all(`, which the
note names — but not of a bare-name search. The alias is
`resolve_all_annotations`, which *contains* `resolve_all` as a prefix, so plain
`rg resolve_all` matches the call site directly and reaches `annotate` on the
first try.

A02 is therefore **not** a case where grep fails. It is a tie on recall, at 4×
the calls and 2× the bytes. The note is left as written — it is a
pre-registration record and rewriting it after seeing results is exactly what
pre-registration exists to prevent — and this file is the published correction.

The generalisation: aliasing only defeats a text search when the alias is a
genuinely *different* string. A01 is that case because it was constructed to be.
Real aliases are frequently decorated versions of the original name, and those
grep finds fine. Treat A01 as a demonstration of the mechanism, not as evidence
about how often it bites in practice — one hand-built fixture and one real
repo cannot support that claim, and this harness does not make it.

## Fairness

The baseline is not a strawman, by construction:

- **Real ripgrep**, run in the repo, full regex.
- **A battery, not one pattern** — four declared patterns per probe, unioned.
- **Alias-following**, mechanised: the chained tier greps every alias the first
  tier's output revealed.
- **Free attribution.** Turning a `path:line` hit into the name of the enclosing
  function is work a human or model must do by hand; the harness does it for
  grep at no charge, by indentation-aware backward scan.
- **Add your own patterns**: `-grep-pattern 'your regex'`, repeatable. If a
  pattern you think of beats these, the number moves.
- **Every call is logged** in `results/ceiling/ceiling.md` with its byte count,
  so the run can be audited rather than trusted.

max-context is scored the same way: every `name` its responses contain, at any
depth, counts as predicted — and it is charged for the bytes that took.

## Why the fixture is committed

The `payments` repo previously lived at `/tmp/alias-bench/payments` on the
machine that authored it. It did not survive that machine, which made the
0/5-vs-5/5 result unreproducible by anyone, including its author. It is now at
`fixtures/payments/`, and `internal/ceiling/fixture_test.go` pins its exact file
paths, line numbers, and alias names to the oracle in the answer key — so the
key cannot start silently grading a different repo. A separate test asserts that
no call site names `post_transaction` directly, which is the property the whole
probe depends on.

Moving it edited `alias-v4.json`, a pre-registered file.
`TestFixturePathDoesNotPerturbThePreRegistration` proves the edit was confined
to `clone_url`, which sits outside the hashed subset — the tasks, keys, and
models that grading depends on hash identically before and after.
