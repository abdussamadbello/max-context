# Language support matrix

max-context ships tree-sitter grammars for the languages below. Support comes in
two distinct layers, and they are **not** the same:

- **Parsed (symbols extracted)** — what the indexer pulls out of the source and
  makes searchable via `get_definition` / `query_codebase`: functions, types,
  imports, and call sites.
- **Resolved (call-graph precision)** — how precisely a call site is linked to
  its target in the call graph that powers `get_call_chain` and `get_impact`.
  Each linked edge carries a *resolution marker* indicating confidence.

Only **Go, TypeScript/TSX, and Python** have a dedicated scope resolver. Every
other language is parsed with the generic extractor and its call edges resolve
by **global name match only** (`name-global`) — a single same-named definition
anywhere in the repo. That is honest but coarse: it can mislink when a name is
defined in several places and it never uses receiver/type information.

This file is derived from the actual tree-sitter queries in
`pkg/treesitter/queries/`, the parser dispatch in `internal/indexer/parser.go`,
and the resolver strategies in `internal/indexer/resolver.go`. If you change any
of those, update this table.

## Matrix

| Language | Extensions | Parser | Functions/methods | Types | Imports | Call edges | Resolution |
|---|---|---|---|---|---|---|---|
| Go | `.go` | `parseGo` | ✅ (with receiver types) | ✅ | ✅ | ✅ (with receiver context) | **Full** scope resolver |
| TypeScript | `.ts`, `.tsx` | `parseTS` | ✅ (with `this`/class fields) | ✅ | ✅ | ✅ (with receiver context) | **Full** scope resolver |
| Python | `.py`, `.pyi` | `parsePython` | ✅ (with `self`/type hints) | ✅ (classes) | ✅ | ✅ (with receiver context) | **Full** scope resolver (+ class inheritance) |
| JavaScript | `.js`, `.mjs`, `.cjs` | generic | ✅ | ❌ | ✅ | ✅ (no receiver info) | `name-global` only |
| JSX | `.jsx` | generic | ✅ | ❌ | ✅ | ✅ (no receiver info) | `name-global` only |
| Rust | `.rs` | generic | ✅ | ✅ | ✅ | ✅ (no receiver info) | `name-global` only |
| Java | `.java` | generic | ✅ | ❌ | ✅ | ✅ (no receiver info) | `name-global` only |
| C | `.c`, `.h` | generic | ✅ | ❌ | ✅ | ✅ (no receiver info) | `name-global` only |
| C++ | `.cc`, `.cpp`, `.cxx`, `.hpp`, `.hh`, `.hxx` | generic | ✅ | ✅ | ✅ | ✅ (no receiver info) | `name-global` only |
| Ruby | `.rb` | generic | ✅ | ❌ | ❌ | ✅ (no receiver info) | `name-global` only |
| Swift | `.swift` | generic | ✅ | ✅ | ❌ | ❌ (no call sites extracted) | — |

Legend: ✅ extracted · ❌ not extracted. "no receiver info" means call edges carry
only the callee name, so resolution falls back to `name-global`.

## Resolution markers

A call edge's `resolution` records how its callee was linked, highest to lowest
confidence. Which markers can fire depends on the language tier.

| Marker | Meaning | Languages |
|---|---|---|
| `same-file` | bare call, target defined in the same file | Go, TS/TSX, Python |
| `same-package` | bare call, target elsewhere in the same package/module | Go, TS/TSX, Python |
| `receiver-typed` | method call whose receiver type is statically known | Go, TS/TSX, Python |
| `cross-package` | `pkg.Func()` linked to a unique `Func` in the imported package | Go |
| `import-symbol` | bare call to a `from m import f [as g]` name, linked cross-file | Go, TS/TSX, Python |
| `import-qualified` | `pkg.Foo()` recognized but not uniquely linked | Go, TS/TSX, Python |
| `constructor` | bare call to a known class/struct (classified, not a missed func) | Go, TS/TSX, Python |
| `builtin` | a predeclared/global builtin (`make`, `len`, `print`, …) | Go, TS/TSX, Python |
| `interface-dispatch` | low-confidence fan-out from an interface method to concrete impls | Go only |
| `name-global` | single global name lookup (the generic fallback) | JS, JSX, Rust, Java, C, C++, Ruby |
| `unresolved` | a precise lookup was attempted but found no confident target | Go, TS/TSX, Python |

### Notes and known imprecision

- **`interface-dispatch`** is detected only for Go interfaces (the indexer
  recognizes an interface by a type definition beginning with `interface`). It is
  *name-only* — a concrete type satisfies an interface when its method **names**
  cover the interface's, with no arity or parameter-type check — so it can
  over-approximate. `get_impact` therefore excludes these edges by default and
  includes them only at a low `min_confidence` (`min_confidence=interface-dispatch`).
- **Generic-tier languages** (everything below Python in the table) get no
  receiver typing, no struct-field/global typing, and no inheritance resolution.
  Their call graph is a best-effort name match.
- **Swift** currently extracts functions and types but **no call sites**, so it
  contributes searchable symbols but no call graph.
- Cross-package resolution in a monorepo with multiple modules, and deeper
  resolution for the generic-tier languages, are known gaps (see
  `HARDENING_NOTES.md`).
