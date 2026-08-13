# Benchmark — zod

**Question count:** 11

| Path | Avg tokens | Savings vs max-context |
|---|---|---|
| max-context | 1011 | 1× |
| Naive Grep+Read | 223622 | 221.2× |
| Skilled Grep+Read | 94745 | 93.7× |

## Per-question

| ID | Category | Text | MC | Naive | Skilled |
|---|---|---|---|---|---|
| I01 | impact | If I change v3/types.ts, what is affected? | 4145 | 126359 | 16637 |
| I02 | impact | If I change v4/core/api.ts, what is affected? | 925 | 83094 | 3562 |
| I03 | impact | If I change classic/schemas.ts, what is affected? | 1125 | 1923582 | 897890 |
| L01 | lookup | Where is addIssueToContext defined? | 317 | 44262 | 14343 |
| L02 | lookup | Where is convertSchema defined? | 306 | 6028 | 3369 |
| L03 | lookup | Where is isTransforming defined? | 310 | 29403 | 1692 |
| L04 | lookup | Where is _addCheck defined? | 350 | 42491 | 3804 |
| L05 | lookup | Where is $constructor defined? | 1055 | 111842 | 79387 |
| T01 | trace | What calls addIssueToContext? | 985 | 44262 | 14343 |
| T02 | trace | What calls _addCheck? | 1344 | 42491 | 3804 |
| T03 | trace | What calls convertSchema? | 259 | 6028 | 3369 |
