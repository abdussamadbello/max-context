# Retrieval ceiling — ceiling-v1

Measures what each arm's TOOLS surface, with no model in the loop.
Recall is the share of hand-verified gold callers that appear in tool output;
an arm that cannot surface a caller sets a ceiling no model can beat.

## A01 — payments

List every function that calls post_transaction.

Gold callers (5): apply_late_fee, charge_subscription, issue_refund, pay_bonus, pay_salary

| Arm | Recall | Precision | Tool calls | Output bytes | Missed |
|---|---|---|---|---|---|
| grep(one-shot) | 0/5 | 0.00 | 4 | 518 | apply_late_fee, charge_subscription, issue_refund, pay_bonus, pay_salary |
| grep(alias-chained) | 5/5 | 1.00 | 6 | 886 | — |
| max-context | 5/5 | 1.00 | 1 | 660 | — |

<details><summary>Every call that was run</summary>

- `rg post_transaction` → grep(one-shot) (197 bytes)
- `rg post_transaction\s*\(` → grep(one-shot) (62 bytes)
- `rg \bpost_transaction\b` → grep(one-shot) (197 bytes)
- `rg def post_transaction` → grep(one-shot) (62 bytes)
- `rg post_transaction` → grep(alias-chained) (197 bytes)
- `rg post_transaction\s*\(` → grep(alias-chained) (62 bytes)
- `rg \bpost_transaction\b` → grep(alias-chained) (197 bytes)
- `rg def post_transaction` → grep(alias-chained) (62 bytes)
- `rg apply_entry\s*\(` → grep(alias-chained) (224 bytes)  
  follow-up: `post_transaction` is imported as `apply_entry`
- `rg record_payment\s*\(` → grep(alias-chained) (144 bytes)  
  follow-up: `post_transaction` is imported as `record_payment`
- `get_call_chain {
            "function_name": "post_transaction",
            "direction": "callers",
            "depth": 3
          }` → max-context (660 bytes)

</details>

## A02 — celery

List every function or method that calls resolve_all, the task-annotation resolver defined in celery/app/annotations.py.

Gold callers (1): annotate

| Arm | Recall | Precision | Tool calls | Output bytes | Missed |
|---|---|---|---|---|---|
| grep(one-shot) | 1/1 | 0.50 | 4 | 784 | — |
| grep(alias-chained) | 1/1 | 0.50 | 5 | 877 | — |
| max-context | 1/1 | 0.50 | 1 | 390 | — |

<details><summary>Every call that was run</summary>

- `rg resolve_all` → grep(one-shot) (431 bytes)
- `rg resolve_all\s*\(` → grep(one-shot) (60 bytes)
- `rg \bresolve_all\b` → grep(one-shot) (233 bytes)
- `rg def resolve_all` → grep(one-shot) (60 bytes)
- `rg resolve_all` → grep(alias-chained) (431 bytes)
- `rg resolve_all\s*\(` → grep(alias-chained) (60 bytes)
- `rg \bresolve_all\b` → grep(alias-chained) (233 bytes)
- `rg def resolve_all` → grep(alias-chained) (60 bytes)
- `rg resolve_all_annotations\s*\(` → grep(alias-chained) (93 bytes)  
  follow-up: `resolve_all` is imported as `resolve_all_annotations`
- `get_call_chain {
            "function_name": "resolve_all",
            "direction": "callers",
            "depth": 3
          }` → max-context (390 bytes)

</details>

