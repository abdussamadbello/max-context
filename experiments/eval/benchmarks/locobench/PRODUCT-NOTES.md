# Product positioning notes — surfaced by the LoCoBench eval

These are product/positioning takeaways the LoCoBench work produced. They are
*recommendations*, deliberately separate from engine code changes. See
`FINDINGS.md` for the evidence.

## 1. max-context is additive by design — say so explicitly

The eval's pure max-context arm (only the 5 MCP tools, no file reading) measures a
configuration **nobody deploys**. In every real MCP host — Claude Code, Cursor,
Windsurf, Copilot — installing max-context *adds* tools; it never removes the
agent's built-in `Read`/`Grep`/`Glob`. So the honest comparison is:

    grep+read   (baseline)   vs   grep+read+mc   (max-context installed)

not "grep vs mc". The product's value is its **marginal contribution on top of the
file tools an agent already has** — faster/cheaper navigation, call-graph and
impact queries — not being a self-sufficient replacement for reading files.

**Action (docs/positioning, no code):** state in the README / tool descriptions
that max-context complements the host's file tools and assumes they exist. This
reframes the coverage gap below from "max-context can't see these files" (sounds
like a defect) to "the host reads those files; max-context indexes the code" (a
clean division of labor).

## 2. The coverage gap is real — but the fix is targeted, not "index everything"

Verified statically across all 8,000 LoCoBench scenarios (`cmd/coverage`):
max-context indexes only tree-sitter code languages, so it is blind to non-code
artifacts (.md, .yaml, .json, .proto, GraphQL SDL, SQL, …) — present in ~87% of
supported-language tasks — and to unsupported code languages (C#, PHP: 20%).

Two candidate responses, with my recommendation:

- **(Recommended) Discoverability-only indexing of non-code files.** Index file
  *paths* + maybe a light keyword/FTS layer for non-code files so `query_codebase`
  can *surface* that `schema.graphqls` or `config.yaml` exists and is relevant —
  then the agent reads it with its own file tool. This fixes the real failure
  ("the agent never knew the schema file existed") without max-context reparsing
  or re-serving content the host already reads. Small, targeted.
- **(Not recommended) Full text indexing of all non-code content.** Re-serves what
  every host can already read, bloats the index, competes with better host tools.
  Scope creep.
- **(Niche) A read/grep fallback tool on the MCP server.** Only defensible for
  MCP-only environments where max-context is genuinely the sole tool. Redundant in
  a normal host; don't lead with it.

## 3. Unsupported languages (C#, PHP) are a scope statement, not a bug

20% of LoCoBench is C#/PHP, which have no tree-sitter binding wired here. This
matches the documented language set. No action beyond being explicit about
supported languages; add bindings only if those languages are a target market.

---

*Status: recommendations pending review. No engine code changed on the basis of
these notes; the eval-side hybrid arm (grep+read+mc) was added to MEASURE point 1.*
