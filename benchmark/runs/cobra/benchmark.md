# Benchmark — cobra

**Question count:** 11

| Path | Avg tokens | Savings vs max-context |
|---|---|---|
| max-context | 2018 | 1× |
| Naive Grep+Read | 104277 | 51.7× |
| Skilled Grep+Read | 29109 | 14.4× |

## Per-question

| ID | Category | Text | MC | Naive | Skilled |
|---|---|---|---|---|---|
| I01 | impact | If I change command.go, what is affected? | 12559 | 355484 | 88563 |
| I02 | impact | If I change args.go, what is affected? | 2616 | 123331 | 10505 |
| I03 | impact | If I change flag_groups.go, what is affected? | 638 | 49782 | 3664 |
| L01 | lookup | Where is Execute defined? | 220 | 86720 | 15022 |
| L02 | lookup | Where is AddCommand defined? | 313 | 108290 | 37770 |
| L03 | lookup | Where are flags parsed? | 303 | 25769 | 2372 |
| L04 | lookup | Where is bash completion generated? | 323 | 63923 | 4654 |
| L05 | lookup | Where are args validated? | 288 | 15927 | 589 |
| T01 | trace | What calls executeCommand? | 1616 | 72439 | 57812 |
| T02 | trace | What calls AddCommand? | 1667 | 108290 | 37770 |
| T03 | trace | What calls Flags? | 1650 | 137089 | 61475 |
