# Benchmark — .

**Question count:** 20

| Path | Avg tokens | Savings vs max-context |
|---|---|---|
| max-context | 229 | 1× |
| Naive Grep+Read | 41344 | 180.3× |
| Skilled Grep+Read | 13122 | 57.2× |

## Per-question

| ID | Category | Text | MC | Naive | Skilled |
|---|---|---|---|---|---|
| I01 | impact | If I change internal/db/queries.go, what's affected? | 78 | 70601 | 18340 |
| I02 | impact | If I refactor RegisterAll, what's affected? | 99 | 14775 | 4693 |
| I03 | impact | If I change SymbolsInFile, what calls it? | 99 | 21287 | 6824 |
| I04 | impact | If I change the calls table schema, what code needs updating? | 99 | 125476 | 56837 |
| I05 | impact | If I rename queryCallChain, what's affected? | 99 | 7899 | 3368 |
| I06 | impact | If I change the MCP handler signature, what needs to be updated? | 99 | 30022 | 4186 |
| I07 | impact | If I touch internal/tools/register.go, what's affected? | 78 | 13510 | 3665 |
| L01 | lookup | Where is the function that opens the SQLite database? | 776 | 80638 | 27152 |
| L02 | lookup | Where is the file watcher started? | 432 | 18900 | 6606 |
| L03 | lookup | Which file defines the MCP tool handler signature? | 179 | 15200 | 3907 |
| L04 | lookup | Where is the query_codebase tool registered? | 1174 | 79414 | 24077 |
| L05 | lookup | What is the schema migration for the functions table? | 165 | 9839 | 1307 |
| L06 | lookup | Where is the architecture summary generated? | 533 | 25233 | 2349 |
| L07 | lookup | What's the structure of a search result? | 174 | 6948 | 3709 |
| T01 | trace | What calls IndexFile? | 72 | 56846 | 15816 |
| T02 | trace | What does Migrate call? | 134 | 60075 | 21082 |
| T03 | trace | What calls PrepareQueries? | 100 | 42511 | 13857 |
| T04 | trace | What calls Open in the db package? | 49 | 85711 | 19826 |
| T05 | trace | What does Index() in indexer call? | 50 | 6103 | 1312 |
| T06 | trace | Trace what the MCP Serve method ends up doing | 97 | 55886 | 23517 |
