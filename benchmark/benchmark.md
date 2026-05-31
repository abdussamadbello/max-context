# Benchmark — max-context-core

**Question count:** 20

| Path | Avg tokens | Savings vs max-context |
|---|---|---|
| max-context | 229 | 1× |
| Naive Grep+Read | 30911414 | 134807.7× |
| Skilled Grep+Read | 11212 | 48.9× |

## Per-question

| ID | Category | Text | MC | Naive | Skilled |
|---|---|---|---|---|---|
| I01 | impact | If I change internal/db/queries.go, what's affected? | 78 | 47499559 | 17961 |
| I02 | impact | If I refactor RegisterAll, what's affected? | 99 | 23858765 | 5322 |
| I03 | impact | If I change SymbolsInFile, what calls it? | 99 | 44993 | 12863 |
| I04 | impact | If I change the calls table schema, what code needs updating? | 99 | 71101735 | 42071 |
| I05 | impact | If I rename queryCallChain, what's affected? | 99 | 23855765 | 3660 |
| I06 | impact | If I change the MCP handler signature, what needs to be updated? | 99 | 47203247 | 4127 |
| I07 | impact | If I touch internal/tools/register.go, what's affected? | 78 | 47449472 | 6503 |
| L01 | lookup | Where is the function that opens the SQLite database? | 776 | 23929913 | 23627 |
| L02 | lookup | Where is the file watcher started? | 432 | 23904762 | 11615 |
| L03 | lookup | Which file defines the MCP tool handler signature? | 179 | 23601853 | 3870 |
| L04 | lookup | Where is the query_codebase tool registered? | 1174 | 23626186 | 31729 |
| L05 | lookup | What is the schema migration for the functions table? | 165 | 23881144 | 2676 |
| L06 | lookup | Where is the architecture summary generated? | 533 | 47714064 | 3053 |
| L07 | lookup | What's the structure of a search result? | 174 | 23587145 | 2803 |
| T01 | trace | What calls IndexFile? | 72 | 23864146 | 4914 |
| T02 | trace | What does Migrate call? | 134 | 23881936 | 11045 |
| T03 | trace | What calls PrepareQueries? | 100 | 23871472 | 9023 |
| T04 | trace | What calls Open in the db package? | 49 | 47582135 | 10453 |
| T05 | trace | What does Index() in indexer call? | 50 | 23853616 | 1986 |
| T06 | trace | Trace what the MCP Serve method ends up doing | 97 | 23916369 | 14935 |
