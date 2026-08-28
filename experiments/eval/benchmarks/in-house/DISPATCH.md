# Interface dispatch — a hypothesis, and its refutation

```
cd experiments/eval/benchmarks/in-house
go run ./cmd/ceiling -protocol protocol/ceiling-v2-dispatch.json -keys protocol/dispatch-v5.json
```

No API key, no model, no cost. Same harness as [CEILING.md](CEILING.md).

## The hypothesis

Aliasing (v4) is the one place text search structurally breaks, but it is **rare**
— one qualifying case in 16 repos surveyed. Interface dispatch looked like the
same mechanism at far greater frequency: `n.Send(msg)` runs `EmailNotifier.Send`,
and the call site names neither the type nor the file. Every language with
interfaces has it. If grep were blind to it the way it is blind to aliases, that
would be a common structural win rather than a rare one.

**It is not.** The probe refuted this before any resolver work was funded, which
is what a cheap probe is for.

## Results (`ceiling-v2-dispatch`, run 2026-08-27)

Fixture `fixtures/dispatch`. `Notifier` has one method, `Send`. `EmailNotifier`
and `SMSNotifier` implement it; `DeliverAlert` and `BroadcastAll` dispatch
through the interface and are the gold set. `MetricsBuffer` is a decoy: it has a
`Send` method, is never used as a `Notifier`, and `FlushMetrics` calls it on the
concrete type.

| Probe | Arm | Recall | Precision | Calls | Bytes |
|---|---|---|---|---|---|
| D01 | grep (one-shot) | **1.00** | 0.67 | 4 | 930 |
| D01 | max-context (shipped default) | **0.00** | 0.00 | 1 | 256 |
| D02 | grep (one-shot) | **1.00** | 0.67 | 4 | 930 |
| D02 | max-context (`min_confidence: interface-dispatch`) | **0.50** | 0.50 | 1 | 359 |

**max-context loses on this probe, at both settings.** It is cheaper — 1 call and
a quarter of the bytes — and it is wrong.

### After the fixes below (run 2026-08-28)

| Probe | Arm | Recall | Precision | Calls | Bytes |
|---|---|---|---|---|---|
| D01 | grep (one-shot) | 1.00 | 0.67 | 4 | 930 |
| D01 | max-context (**shipped default**) | **1.00** | **0.67** | **1** | **463** |
| D02 | max-context (`min_confidence: interface-dispatch`) | 1.00 | 0.67 | 1 | 463 |

Out of the box, with no flags, max-context now returns **exactly grep's answer
set** — same recall, same precision, same three names — in 1 call instead of 4
and 463 bytes instead of 930. The loss is gone; what is left is the
ties-on-quality, wins-on-cost shape this project's other results have.

D01 and D02 now agree, which is the point: the setting that produced the right
answer is the one users get without knowing it exists.

### D03 — addressing one definition by symbol (run 2026-08-28)

Every arm above caps at **precision 0.67**, and the reason is not retrieval: the
question cannot be asked. Three types here declare `Send`, `get_call_chain` took
only a bare `function_name`, and grep matches text — so `MetricsBuffer.Send`
comes back no matter which `Send` the caller meant.

D03 asks the same question with the max-context arm naming one definition by a
SCIP-shaped symbol instead:

```json
{"symbol": "go . dispatch . EmailNotifier#Send().", "direction": "callers"}
```

| Arm | Recall | Precision | Calls | Bytes |
|---|---|---|---|---|
| grep (one-shot) | 1.00 | 0.67 | 4 | 930 |
| grep (alias-chained) | 1.00 | 0.67 | 4 | 930 |
| **max-context (by symbol)** | **1.00** | **1.00** | **1** | **413** |

This is the first probe in the series where max-context **beats** grep rather
than matching it. The gain is not a better search — it is a question grep has no
way to express. A text search cannot say "this `Send`, not that one", so it pays
the decoy at every confidence and with every pattern.

Symbols are recorded per definition at index time from the package, receiver
type, and kind already stored (`internal/indexer/symbolid.go`), following
[SCIP's symbol grammar](https://github.com/scip-code/scip/blob/main/docs/scip.md).
This is **not** a SCIP index: there is no dependency resolution, so manager and
version are placeholders and a symbol is unique within one indexed repository
rather than across repositories.

One honest limit the probe exposes. Asking for
`go . dispatch . MetricsBuffer#Send().` returns all three callers, not one.
That is correct: `MetricsBuffer` has a `Send` method with the right signature, so
it structurally satisfies `Notifier`, and a `Notifier` value could dispatch to
it. Nothing in the source rules that out — only the fact that no caller ever
passes one, which needs call-site type flow the indexer does not do. Symbols
sharpen the *definition* side of the question; they do not narrow the fan-out.

## Satisfaction is now its own relation

That limit was only *visible* as a puzzle — why does the decoy appear? —
because satisfaction had no representation of its own. It existed as an
in-memory memo whose sole output was a fan-out of synthetic call edges, so "what
implements this interface?" could only be answered by asking who calls it and
reading the fan-out back out of the answer. That is also why the fan-out needed
a width gate: it was riding inside a caller list that had to stay usable.

SCIP keeps these apart deliberately —
[`Relationship.is_implementation`](https://github.com/scip-code/scip/blob/main/scip.proto)
is a distinct fact from a reference, so "find implementations" and "find
references" never answer each other. The same separation now exists here:
migration 12 stores an `implementations` relation, and `get_call_chain` takes
`direction: "implementations"`.

```
$ max-context tool get_call_chain --json '{"function_name":"Send","direction":"implementations"}'
  Notifier <- EmailNotifier   email.go:8
  Notifier <- MetricsBuffer   metrics.go:15
  Notifier <- SMSNotifier     sms.go:10
```

The decoy's presence in the caller list is now *explained* by a stored fact
rather than inferred from the shape of an answer to a different question.

**Cost:** none measurable. `max-context bench` reports 36.5× vs naive and 11.3×
vs skilled, unchanged from before symbols and the implementations relation
existed. Average response tokens moved 1,374 → 1,435, but naive and skilled rose
by the same proportion (+4.3% and +3.8%) over the same interval: that is the
repository growing by 529 lines between runs, not a regression. Ratios are the
comparable figure; the absolute token counts are not, across runs.

## Why the hypothesis was wrong (still true after the fixes)

Aliasing and interface dispatch hide *different* things, and only one of them is
the thing grep reads.

- **Alias:** `post_transaction` imported as `apply_entry`; the call site reads
  `apply_entry(...)`. The target's name is **absent from the call site**. Grep
  has nothing to match.
- **Interface dispatch:** the call site reads `n.Send(msg)`. The *receiver type*
  is hidden, but the **method name is right there**. Grep matches `\.Send\(`
  immediately and attributes both callers.

So interface dispatch costs grep **precision**, not recall — it cannot tell
`Notifier.Send` from `MetricsBuffer.Send`, so it returns the decoy too (0.67).
That is a much weaker claim than "grep cannot find this at all", and it is not a
moat.

## What the probe did find: three defects

Losing to grep here was a bug, not a market. Each was independently reproducible,
and all three are now fixed.

**1. The shipped default returned zero true callers.** *(fixed — the default is
now width-gated)* `get_call_chain` and `get_impact` both excluded every
`interface-dispatch` edge. On D01 the only thing returned was `FlushMetrics` —
the decoy — for a recall of 0.00. "Finds only the wrong answer" is not a
defensible default.

Flipping it wholesale is not defensible either, so the question was settled with
measurement rather than taste. Indexing three real Go repositories (cobra, gin,
client_golang) shows dispatch edges are **0–4% of the call graph**, and their
fan-out width is **bimodal**:

| Fan-out width | Share of call sites | Cumulative |
|---|---|---|
| 1 | 13.4% | 13.4% |
| 2 | 41.1% | 54.5% |
| 3 | 3.6% | 58.0% |
| 5 | 18.8% | **76.8%** |
| 8 | 0.9% | 77.7% |
| 13 | 20.5% | 98.2% |
| 19 | 1.8% | 100% |

Widths cluster at 1–5 and then jump to 13 and 19 with **nothing in between**.
Admitting every edge grew individual responses by a median of 5–87% but by
**872% and 1138%** at the tail — and the blowups were exactly the wide sites.

So the default now admits dispatch edges whose call site fans out to **5
implementations or fewer**, and excludes the rest. The width is recorded per
edge at index time (`dispatch_width`, migration 10), where it is already known,
rather than recomputed by a correlated subquery inside every recursive walk.
Rows from an index predating the column carry 0 and are treated as wide, so an
un-reindexed database keeps its old answers instead of silently widening.

**Measured cost of the new default**, on max-context's own repo via
`max-context bench`: average response 1,311 → 1,374 tokens (**+4.8%**), savings
37.1× → 36.5× vs naive and 11.6× → 11.3× vs skilled. That is the price of D01
going from recall 0.00 to 1.00.

Wide fan-outs are still excluded, but no longer in silence. When the filter
hides edges for the queried symbol, the response says so and names the argument
that reveals them:

```json
"interface_dispatch_excluded": 182,
"interface_dispatch_hint": "182 edge(s) reach Bind through an interface whose
  fan-out is too wide (>5 implementations) to include by default. Re-run with
  min_confidence \"interface-dispatch\" to include them."
```

An empty caller list and "no callers exist" were indistinguishable before. This
follows the same rule the truncation path already had: never narrow an answer
without saying so.

**2. Only 2 of 5 receiver-binding forms resolved.** *(fixed)* With the flag on,
measured against a fixture holding every form:

| Binding | Example | Was | Now |
|---|---|---|---|
| Interface-typed parameter | `func F(n Notifier)` → `n.Send()` | yes | yes |
| Explicit local declaration | `var n Notifier = &Email{}` | yes | yes |
| Range variable | `for _, n := range ns` (`ns []Notifier`) | **no** | yes |
| Short assignment | `n := ns[0]` | **no** | yes |
| Struct field | `h.n.Send()` | **no** | yes |
| Map range | `for _, n := range m` (`map[string]Notifier`) | **no** | yes |

Resolution handled bindings whose interface type was written down and missed
every form requiring inference — which covers the ordinary ways interfaces are
held. Two root causes:

- **No element types.** `ns []Notifier` was not captured at all: the Go query
  matched `type_identifier` and `pointer_type` but no `slice_type`, `array_type`,
  or `map_type`. A range variable over `ns` therefore had nothing to infer from.
  Element types are now captured separately from the identifier's own type — `ns`
  is a slice, not a `Notifier`, and typing it as one would invent a call the code
  never makes.
- **Field receivers never tried dispatch.** The fan-out was gated on
  `ReceiverKind == "var"`, and a field receiver records the type of the *base*
  (`Holder`), not of the field. The field's declared type is now resolved before
  the interface check.

A binding whose source type is unknown still produces no edge rather than a
guess; `TestUnresolvableRangeProducesNoEdge` pins that.

**3. A single-line interface declaration disabled resolution entirely.** *(fixed)*

```go
type Notifier interface{ Send(msg string) error }   // 0 callers resolved
type Notifier interface {                           // resolves
    Send(msg string) error
}
```

Same semantics, formatting-only difference, and the interface's method set was
never extracted in the first form — so no concrete type satisfied it and its
dispatch fan-out was empty. Confirmed on two otherwise-identical trees.

`ifaceMethodRe` in `internal/indexer/resolver.go` anchored a method declaration
to the start of a line, and in the single-line form the method follows `{` on
the same line. It now also accepts a method after the opening brace or after a
semicolon separating methods, so `interface{ Send(m string) error; Close() error }`
yields both. `TestExtractInterfaceMethods` covers all the declaration forms; the
function previously had no test at all, which is how the bug shipped.

This does not move the numbers in the table above — the fixture declares
`Notifier` across multiple lines, and `TestDispatchInterfaceIsDeclaredMultiLine`
keeps it that way so the probe measures dispatch resolution rather than this
bug. It was a silent whole-class failure in real repositories: every interface
written on one line resolved nothing, with nothing in the output to say why.

All three defects are fixed.

## An API gap this exposed

`get_call_chain` takes a bare `function_name`. Three different types here declare
`Send`, and there is no way to ask about one of them — no receiver or type
qualifier in the schema. The decoy is unavoidable for *both* arms partly because
the question cannot be posed precisely. Any fix to interface resolution runs into
this: resolving `n.Send()` to `EmailNotifier.Send` is not useful if callers still
cannot ask for `EmailNotifier.Send`.

## A harness bug this run fixed

The first run of this probe scored grep **0.00** on both probes. That was wrong.
`enclosingFunc` matched only Python (`^\s*def\s+`), so every hit in a `.go` file
attributed to `""` and was dropped — the baseline scored zero no matter what it
found. A harness gap and a genuine retrieval failure print identically.

Fixed in `attribute.go`: `defPatternFor` selects a per-language pattern, and hits
in a language with no pattern now set an explicit error on the arm
("this arm's recall is a floor, not a measurement") rather than silently scoring
as misses. `attribute_go_test.go` covers Go attribution, including that
`func (e *EmailNotifier) Send(...)` captures `Send` and not the receiver.

Publishing the 0.00 would have been this project's second withdrawn number.

## Honest scope

One hand-authored fixture, one language, no model in the loop. It establishes
that the *mechanism* does not defeat grep, and that three specific defects exist.
It does not measure how often interface dispatch matters in real code, and no
frequency claim rests on it — the same caveat `fixtures/payments` carries.
