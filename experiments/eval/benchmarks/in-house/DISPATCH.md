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

## Why the hypothesis was wrong

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

**1. The shipped default returns zero true callers.** `get_call_chain` and
`get_impact` both exclude `interface-dispatch` edges by default
(`get_call_chain.go:136`, `get_impact.go:292`). On D01 the only thing returned is
`FlushMetrics` — the decoy — for a recall of 0.00. Whatever the right default is,
"finds only the wrong answer" is not it.

**2. Only 2 of 5 receiver-binding forms resolve.** With the flag on, measured
directly against a fixture holding all five:

| Binding | Example | Resolves |
|---|---|---|
| Interface-typed parameter | `func F(n Notifier)` → `n.Send()` | yes |
| Explicit local declaration | `var n Notifier = &Email{}` | yes |
| Range variable | `for _, n := range ns` (`ns []Notifier`) | **no** |
| Struct field | `h.n.Send()` | **no** |
| Short assignment | `n := ns[0]` | **no** |

This is why D02 scores 0.50 and not 1.00: `BroadcastAll` ranges over
`[]Notifier`. Resolution handles bindings whose interface type is written down
and misses every form that requires inferring it.

**3. A single-line interface declaration disables resolution entirely.**

```go
type Notifier interface{ Send(msg string) error }   // 0 callers resolved
type Notifier interface {                           // resolves
    Send(msg string) error
}
```

Same semantics, formatting-only difference, and the interface's method set is
never extracted in the first form. Confirmed on two otherwise-identical trees.

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
