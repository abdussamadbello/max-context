# Benchmark — .

**Question count:** 20

| Path | Avg tokens | Savings vs max-context |
|---|---|---|
| max-context | 1214 | 1× |
| Naive Grep+Read | 43158 | 35.5× |
| Skilled Grep+Read | 13671 | 11.3× |

## Per-question

| ID | Category | Text | MC | Naive | Skilled |
|---|---|---|---|---|---|
| I01 | impact | If I change internal/db/queries.go, what's affected? | 3677 | 73014 | 19613 |
| I02 | impact | If I refactor RegisterAll, what's affected? | 810 | 15341 | 5013 |
| I03 | impact | If I change SymbolsInFile, what calls it? | 3677 | 21529 | 6779 |
| I04 | impact | If I change the calls table schema, what code needs updating? | 4565 | 129109 | 57514 |
| I05 | impact | If I rename queryCallChain, what's affected? | 724 | 8661 | 3360 |
| I06 | impact | If I change the MCP handler signature, what needs to be updated? | 1384 | 34768 | 5005 |
| I07 | impact | If I touch internal/tools/register.go, what's affected? | 810 | 14310 | 4262 |
| L01 | lookup | Where is the function that opens the SQLite database? | 313 | 86558 | 28934 |
| L02 | lookup | Where is the file watcher started? | 328 | 19201 | 6656 |
| L03 | lookup | Which file defines the MCP tool handler signature? | 213 | 17581 | 4662 |
| L04 | lookup | Where is the query_codebase tool registered? | 414 | 81796 | 26341 |
| L05 | lookup | What is the schema migration for the functions table? | 348 | 10340 | 1293 |
| L06 | lookup | Where is the architecture summary generated? | 347 | 26261 | 2342 |
| L07 | lookup | What's the structure of a search result? | 342 | 7249 | 3705 |
| T01 | trace | What calls IndexFile? | 459 | 57148 | 15565 |
| T02 | trace | What does Migrate call? | 371 | 62223 | 21830 |
| T03 | trace | What calls PrepareQueries? | 1778 | 44612 | 14886 |
| T04 | trace | What calls Open in the db package? | 1765 | 89895 | 20576 |
| T05 | trace | What does Index() in indexer call? | 1405 | 6609 | 1295 |
| T06 | trace | Trace what the MCP Serve method ends up doing | 556 | 56957 | 23785 |
