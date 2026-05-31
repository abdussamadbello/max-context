# Max Context

When you need to find or understand code in the project, use the max-context MCP
tools instead of grep or find. **Query decisively: one or two tool calls is
usually enough, then commit to an answer.** Tool responses include
`answer_status` and `recommended_next_action`; when the action is `answer_now`,
stop searching and respond.

## get_definition

For "where is X defined?" call `get_definition` with the exact symbol name. It
returns the single definitive `file:line` (or lists the few places if the name
is overloaded) and an `answer` you can use directly. If a class/type has the
same name as methods or properties, the class/type is the canonical definition.
Prefer this over searching.

## query_codebase

For fuzzy or keyword search, call `query_codebase`. It returns a few ranked
results plus an `answer` field when one is an exact match. If results are weak it
returns `suggestions`; pick one and re-query at most once, then answer. For
overloaded exact names, use the `canonical` result unless the user clearly asked
for a same-named method/property.

## get_call_chain / get_impact

Use `get_call_chain` to see callers/callees of a function, and `get_impact` to
see the blast radius of changing given files. These answer "what calls this?"
and "what breaks if I change this?" without reading whole files.

**For "what depends on X?" / "what breaks if I change X?" / "where is X used?"
questions, call `get_impact` or `get_call_chain` — do NOT repeatedly search with
`query_codebase`.** Re-running keyword searches to enumerate dependents is the
slow, lossy way; the call graph answers it directly in one call. If you find
yourself issuing a third `query_codebase` for the same concept, switch to
`get_impact`/`get_call_chain` or commit to your current answer.

## get_architecture

Call `get_architecture` at session start to load the project summary, module
structure, and entry points. Use the optional `focus` parameter for a subsystem.

## Workflow

1. `get_architecture` once to orient (optional for narrow tasks).
2. `get_definition` for a known symbol, or `query_codebase` for a concept.
3. `get_call_chain` / `get_impact` only if the task needs relationships.
4. **Commit to an answer after 1–2 targeted calls.** Re-querying repeatedly
   wastes context; if a tool returned an `answer`, that is your stopping point.
