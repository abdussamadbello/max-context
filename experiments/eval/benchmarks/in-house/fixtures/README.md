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

## Do not edit casually

The answer key in `../protocol/alias-v4.json` cites exact file paths, line
numbers, and alias names from these files. `internal/ceiling/fixture_test.go`
pins all of them, plus the property the probe depends on: that no call site
names `post_transaction` directly. Change a line here and those tests fail —
which is the point. The fixture previously lived in `/tmp` on one machine and
was lost, taking the reproducibility of the published result with it.
