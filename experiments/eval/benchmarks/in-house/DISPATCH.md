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
| D02 | grep (one-shot) | 1.00 | 0.67 | 4 | 930 |
| D02 | max-context (`min_confidence: interface-dispatch`) | **1.00** | **0.67** | **1** | **463** |
| D01 | max-context (shipped default) | 0.00 | 0.00 | 1 | 469 |

Opted in, max-context now returns **exactly grep's answer set** — same recall,
same precision, same three names — in 1 call instead of 4 and 463 bytes instead
of 930. The loss is gone; what is left is the ties-on-quality, wins-on-cost
shape this project's other results have.

D01 still scores 0.00 because the default still excludes the fan-out. It no
longer does so silently: see defect 1.

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

Losing to grep here is a bug, not a market. Each of these is independently
reproducible.

**1. The shipped default returns zero true callers.** *(mitigated, not flipped)*
`get_call_chain` and `get_impact` both exclude `interface-dispatch` edges by
default. On D01 the only thing returned is `FlushMetrics` — the decoy — for a
recall of 0.00. Whatever the right default is, "finds only the wrong answer" is
not it.

The default is unchanged: on a repo with twenty implementations behind one
interface the fan-out is genuinely noisy, and this fixture is too small to
justify flipping it globally. What changed is that it no longer happens in
silence. When the filter hides dispatch edges for the queried symbol, the
response says so and names the argument that reveals them:

```json
"interface_dispatch_excluded": 6,
"interface_dispatch_hint": "6 edge(s) reach Send through an interface and are
  excluded at the default confidence. Re-run with min_confidence
  \"interface-dispatch\" to include them."
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

Defect 1 is mitigated (the exclusion is now reported); defect 2 is fixed.

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
