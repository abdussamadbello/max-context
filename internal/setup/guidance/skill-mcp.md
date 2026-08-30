---
name: max-context
description: Use when finding or understanding code in this project — "where is X defined", "who calls X", "what breaks if I change X", "how does X work", "find the function that does X", or any search for symbols, functions, types, or files. Use instead of grep, find, or reading files to locate code. Also use at session start to orient in an unfamiliar codebase.
---

# Max Context

This project has a pre-built code index. Use the max-context MCP tools instead
of grep or find to locate code. **Query decisively: one or two tool calls is
usually enough, then commit to an answer.** Responses include `answer_status`
and `recommended_next_action`; when the action is `answer_now`, stop searching
and respond.

## get_definition

For "where is X defined?" call `get_definition` with the exact symbol name. It
returns the definitive `file:line` (or the few places if the name is
overloaded) and an `answer` you can use directly. When a class or type shares a
name with methods or properties, the class/type is the canonical definition.
Prefer this over searching.

## query_codebase

For fuzzy or keyword search, call `query_codebase`. Multi-word queries match
symbol names: "resolver cache" finds `ResolverCache`, "remove file" finds
`removeFile`. It returns a few ranked results plus an `answer` when one is an
exact match. If results are weak it returns `suggestions` — pick one and
re-query at most once, then answer.

## get_call_chain / get_impact

`get_call_chain` shows callers and callees of a function. `get_impact` shows the
blast radius of changing given files, defaulting to the diff against HEAD.

**For "what depends on X?" / "what breaks if I change X?" / "where is X used?",
call `get_impact` or `get_call_chain` — do NOT repeatedly search with
`query_codebase`.** Re-running keyword searches to enumerate dependents is the
slow, lossy way; the call graph answers it directly in one call. If you find
yourself issuing a third `query_codebase` for the same concept, switch to
`get_impact`/`get_call_chain` or commit to your current answer.

## get_architecture

Call `get_architecture` to load the project summary, module structure, and entry
points. Use the optional `focus` parameter for a subsystem.

## max-context context (experimental, CLI only)

There is no MCP tool for this one. When a task is broad enough that you would
otherwise call several of the tools above, run the CLI instead and get the
highest-priority evidence that fits a hard budget in one shot:

```bash
max-context context --task "change JWT refresh token expiration" --budget 4000
```

It reports `tokens_used`, whether the package is `complete`, and what was
omitted, so a truncated package is visible rather than silent. It is kept out of
the MCP tool list on purpose: tool schemas are re-sent every turn, and this one
has not yet earned that permanent cost.

## Workflow

1. `get_architecture` once to orient (optional for narrow tasks).
2. `get_definition` for a known symbol, or `query_codebase` for a concept.
3. `get_call_chain` / `get_impact` only if the task needs relationships.
4. **Commit to an answer after 1–2 targeted calls.** If a tool returned an
   `answer`, that is your stopping point.

## If the index is not ready

Run `max-context --index` once in the project root. The index then stays current
automatically as files change.
