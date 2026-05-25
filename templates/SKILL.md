# Max Context

When you need to find code in the project, use the max-context MCP tools instead of grep or find.

## query_codebase

Call `query_codebase` with a natural language or keyword query to search the indexed codebase. Results include file path, line, kind (function/type), name, and a short snippet. Use the `limit` parameter (default 10, max 50) and `scope` (all, functions, types) to narrow results.

## get_architecture

Call `get_architecture` at session start to load the project summary, module structure, and entry points. Use the optional `focus` parameter to restrict to a subsystem.

## Workflow

For multi-file tasks: call get_architecture first, then query_codebase for specific symbols or concepts. Prefer query_codebase over grep to save context.
