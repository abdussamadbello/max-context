# Benchmark — .

**Question count:** 20

| Path | Avg tokens | Savings vs max-context |
|---|---|---|
| max-context | 229 | 1× |
| Naive Grep+Read | 39883 | 173.9× |
| Skilled Grep+Read | 12664 | 55.2× |

## Per-question

| ID | Category | Text | MC | Naive | Skilled |
|---|---|---|---|---|---|
| I01 | impact | If I change internal/db/queries.go, what's affected? | 78 | 70845 | 18279 |
| I02 | impact | If I refactor RegisterAll, what's affected? | 99 | 13756 | 4211 |
| I03 | impact | If I change SymbolsInFile, what calls it? | 99 | 21316 | 6840 |
| I04 | impact | If I change the calls table schema, what code needs updating? | 99 | 119622 | 53982 |
| I05 | impact | If I rename queryCallChain, what's affected? | 99 | 7928 | 3381 |
| I06 | impact | If I change the MCP handler signature, what needs to be updated? | 99 | 30022 | 4186 |
| I07 | impact | If I touch internal/tools/register.go, what's affected? | 78 | 13512 | 3677 |
| L01 | lookup | Where is the function that opens the SQLite database? | 776 | 76928 | 26009 |
| L02 | lookup | Where is the file watcher started? | 432 | 17670 | 6337 |
| L03 | lookup | Which file defines the MCP tool handler signature? | 179 | 15200 | 3907 |
| L04 | lookup | Where is the query_codebase tool registered? | 1174 | 75547 | 24074 |
| L05 | lookup | What is the schema migration for the functions table? | 165 | 9839 | 1307 |
| L06 | lookup | Where is the architecture summary generated? | 533 | 25179 | 2349 |
| L07 | lookup | What's the structure of a search result? | 174 | 6948 | 3709 |
| T01 | trace | What calls IndexFile? | 72 | 55544 | 15544 |
| T02 | trace | What does Migrate call? | 134 | 58788 | 20816 |
| T03 | trace | What calls PrepareQueries? | 100 | 42513 | 13867 |
| T04 | trace | What calls Open in the db package? | 49 | 85657 | 19826 |
| T05 | trace | What does Index() in indexer call? | 50 | 6076 | 1312 |
| T06 | trace | Trace what the MCP Serve method ends up doing | 97 | 44775 | 19677 |
