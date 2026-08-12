---
name: max-context
description: Use when finding or understanding code in this project — "where is X defined", "who calls X", "what breaks if I change X", "how does X work", "find the function that does X", or any search for symbols, functions, types, or files. Use instead of grep, find, or reading files to locate code. Also use at session start to orient in an unfamiliar codebase.
---

# Max Context

This project has a pre-built code index. Query it with the `max-context` command
instead of grepping. Every command prints JSON to stdout. **Query decisively:
one or two calls is usually enough, then commit to an answer.**

| Question | Command |
|---|---|
| Where is X defined? | `max-context def X` |
| Find code by keyword | `max-context query "some words" -n 5` |
| Who calls this? | `max-context calls Name -direction callers` |
| What does this call? | `max-context calls Name -direction callees` |
| What breaks if I change this? | `max-context impact -from-git HEAD` |
| Project overview | `max-context arch` |

Flags may go on either side of the positional argument.

## Reading the response

Every response carries `answer_status` and `recommended_next_action`. When
`answer_status` is `definitive`, answer from that result without searching
again. `get_definition` results include an `answer` string you can use directly.

Multi-word queries match symbol names: `max-context query "resolver cache"`
finds `ResolverCache`; `"remove file"` finds `removeFile`.

## Choosing a command

- **"Where is X defined?"** → `max-context def X`. Prefer this over `query`.
- **"What depends on X?" / "what breaks if I change X?" / "where is X used?"**
  → `max-context impact` for the blast radius of a change, or
  `max-context calls` for one function's callers — do NOT re-run `query`
  repeatedly to enumerate dependents. Re-running keyword searches is the slow,
  lossy way; the call graph answers it directly in one call.
- **A concept rather than a name** → `max-context query "..."`. If results are
  weak the response lists `suggestions`; pick one and re-query at most once.

If you find yourself issuing a third `query` for the same concept, switch to
`impact`/`calls` or commit to your current answer.

## If the index is not ready

Run `max-context --index` once in the project root. The index then stays current
automatically as files change.
