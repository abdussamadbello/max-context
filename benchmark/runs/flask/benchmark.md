# Benchmark — flask

**Question count:** 11

| Path | Avg tokens | Savings vs max-context |
|---|---|---|
| max-context | 397 | 1× |
| Naive Grep+Read | 89638 | 225.6× |
| Skilled Grep+Read | 15498 | 39.0× |

## Per-question

| ID | Category | Text | MC | Naive | Skilled |
|---|---|---|---|---|---|
| I01 | impact | If I change app.py, what is affected? | 1110 | 186388 | 22892 |
| I02 | impact | If I change cli.py, what is affected? | 651 | 30651 | 3605 |
| I03 | impact | If I change blueprints.py, what is affected? | 161 | 273860 | 36356 |
| L01 | lookup | Where is add_url_rule defined? | 371 | 103303 | 19385 |
| L02 | lookup | Where is ensure_sync defined? | 268 | 28505 | 7257 |
| L03 | lookup | Where is load_app defined? | 265 | 13641 | 2637 |
| L04 | lookup | Where is send_file defined? | 310 | 61934 | 13445 |
| L05 | lookup | Where is url_for defined? | 440 | 142291 | 35618 |
| T01 | trace | What calls load_app? | 107 | 13641 | 2637 |
| T02 | trace | What calls add_url_rule? | 254 | 103303 | 19385 |
| T03 | trace | What calls ensure_sync? | 433 | 28505 | 7257 |
