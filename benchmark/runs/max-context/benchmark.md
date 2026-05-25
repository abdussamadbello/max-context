# Benchmark — .

**Question count:** 20

| Path | Avg tokens | Savings vs max-context |
|---|---|---|
| max-context | 229 | 1× |
| Naive Grep+Read | 38930082 | 169777.9× |
| Skilled Grep+Read | 6983 | 30.5× |

## Per-question

| ID | Category | Text | MC | Naive | Skilled |
|---|---|---|---|---|---|
| I01 | impact | If I change internal/db/queries.go, what's affected? | 78 | 57559898 | 12288 |
| I02 | impact | If I refactor RegisterAll, what's affected? | 99 | 28964476 | 3831 |
| I03 | impact | If I change SymbolsInFile, what calls it? | 99 | 28981153 | 10288 |
| I04 | impact | If I change the calls table schema, what code needs updating? | 99 | 86110783 | 16825 |
| I05 | impact | If I rename queryCallChain, what's affected? | 99 | 28965297 | 3211 |
| I06 | impact | If I change the MCP handler signature, what needs to be updated? | 99 | 57163643 | 4101 |
| I07 | impact | If I touch internal/tools/register.go, what's affected? | 78 | 57535014 | 5592 |
| L01 | lookup | Where is the function that opens the SQLite database? | 776 | 29019196 | 11635 |
| L02 | lookup | Where is the file watcher started? | 432 | 29020255 | 6662 |
| L03 | lookup | Which file defines the MCP tool handler signature? | 179 | 28582050 | 3848 |
| L04 | lookup | Where is the query_codebase tool registered? | 1174 | 28582911 | 21732 |
| L05 | lookup | What is the schema migration for the functions table? | 165 | 28988750 | 1928 |
| L06 | lookup | Where is the architecture summary generated? | 533 | 57932328 | 3049 |
| L07 | lookup | What's the structure of a search result? | 174 | 28568019 | 2801 |
| T01 | trace | What calls IndexFile? | 72 | 28969963 | 3179 |
| T02 | trace | What does Migrate call? | 134 | 28970202 | 7129 |
| T03 | trace | What calls PrepareQueries? | 100 | 28968396 | 5726 |
| T04 | trace | What calls Open in the db package? | 49 | 57717312 | 8299 |
| T05 | trace | What does Index() in indexer call? | 50 | 28963202 | 1984 |
| T06 | trace | Trace what the MCP Serve method ends up doing | 97 | 29038791 | 5546 |
