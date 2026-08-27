# Benchmark fixtures

## `payments/` — controlled alias fixture

A hand-authored repo for task A01. `post_transaction` is defined once in
`ledger.py` and imported under two different names:

| File | Import | Callers |
|---|---|---|
| `billing.py` | `post_transaction as apply_entry` | `charge_subscription`, `issue_refund`, `apply_late_fee` |
| `payroll.py` | `post_transaction as record_payment` | `pay_salary`, `pay_bonus` |

Five functions call it. **None of them spell its name**, so a text search for
`post_transaction` reaches the definition and the two import lines and no call
site.

This is a constructed worst case for text search, not a sample of real code —
it exists to isolate one mechanism, and no frequency claim rests on it. See
`../CEILING.md`, which reports the real-repo probe where the same mechanism
does *not* defeat grep.

## `dispatch/` — controlled interface-dispatch fixture

A hand-authored Go repo for tasks D01/D02. `Notifier` declares one method,
`Send`; `EmailNotifier` and `SMSNotifier` implement it.

| File | Role |
|---|---|
| `pipeline.go` | `DeliverAlert` (interface-typed parameter) and `BroadcastAll` (range over `[]Notifier`) — the gold callers |
| `email.go`, `sms.go` | the two implementations, never named at a call site |
| `metrics.go` | `MetricsBuffer.Send` + `FlushMetrics` — the **precision decoy** |

The decoy is the point of the fixture. `MetricsBuffer` has a `Send` method and is
never used as a `Notifier`, so any arm matching on the bare method name surfaces
`FlushMetrics` and loses precision. `BroadcastAll` uses a range variable
deliberately: that binding form is one max-context does not resolve, so the
fixture measures the gap rather than hiding it.

Unlike `payments/`, this fixture does **not** defeat text search — the method
name is present at the call site. See `../DISPATCH.md`, which reports the
refutation and the three defects the probe found.

## Do not edit casually

The answer key in `../protocol/alias-v4.json` cites exact file paths, line
numbers, and alias names from these files. `internal/ceiling/fixture_test.go`
pins all of them, plus the property the probe depends on: that no call site
names `post_transaction` directly. Change a line here and those tests fail —
which is the point. The fixture previously lived in `/tmp` on one machine and
was lost, taking the reproducibility of the published result with it.
