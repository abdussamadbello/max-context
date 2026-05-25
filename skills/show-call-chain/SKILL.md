---
name: show-call-chain
description: Use this skill when the user asks "who calls this function", "what does this function call", "show me the call chain", "trace the call graph", "find callers of", "find callees of", or wants to understand the blast radius of changing a function. Also use when investigating how a function is used across the codebase.
---

# Show Call Chain

Trace the call graph for a function to understand its callers (upstream) and callees (downstream). Uses the `get_call_chain` MCP tool which runs recursive CTEs against the indexed call graph.

## Usage

Call the `get_call_chain` MCP tool with the function name:

### Find who calls a function (callers)
```
get_call_chain(function_name="handleRequest", direction="callers", depth=2)
```

### Find what a function calls (callees)
```
get_call_chain(function_name="handleRequest", direction="callees", depth=2)
```

### Both directions (default)
```
get_call_chain(function_name="handleRequest")
```

## Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `function_name` | string | (required) | Name of the function to trace |
| `direction` | string | `"both"` | `"callers"`, `"callees"`, or `"both"` |
| `depth` | integer | `2` | Recursion depth (1-5). Higher = wider graph |

## Reading Results

Results are grouped by depth level:
- **Depth 1**: Direct callers/callees
- **Depth 2**: Callers of callers / callees of callees
- **`(external)`**: Calls to library functions not in the index

Each node includes `name`, `file_path`, and `line` — enough to navigate directly to the code.

## Common Workflows

1. **Blast radius analysis**: Before changing a function, trace its callers to depth 3 to see all affected code paths.

2. **Dead code detection**: If `get_call_chain` with `direction="callers"` returns empty, the function may be unused.

3. **Dependency understanding**: Trace callees to see what a function depends on before refactoring.

## Prerequisite

The codebase must be indexed first. If `get_call_chain` fails, run `/index-codebase`.
