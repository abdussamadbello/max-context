# Hardening notes

A test-first hardening pass over max-context: one critical data-loss bug and
several design flaws found in review, worked through in phases. Each phase added
a failing test that named the failure mode, then the fix, then confirmed green;
`go test ./...` passes at every phase boundary.

## What was reproduced vs. not

Every claim in the review reproduced against the actual code. Nothing was found
to be wrong, so no plan steps were re-scoped.

| Claim | Reproduced? | Evidence |
|---|---|---|
| Reindexing a cross-file-referenced file violates the FK and silently rolls back | **Yes** | `TestIndexFileReindexesCalleeFile` failed with SQLite error 787 (`FOREIGN KEY constraint failed`) before the fix |
| `RunWorker` swallows `IndexFile`/`Index` errors | **Yes** | `_ = IndexFile(...)` / `_ = Index(...)` in the worker; failures left no trace |
| Agent-style FTS queries misreported as `CodeIndexBusy` | **Yes** | `db.Open(path)`, `retry-logic`, `what calls "Stop"?` all returned "index is rebuilding; retry" (`foo NEAR bar` happened to parse) |
| `NewResolver` is O(repo) per incremental save | **Yes** | Benchmark: 35 ms at 5k funcs → 224 ms at 50k funcs (linear) |
| `get_impact` misses interface dispatch | **Yes** | `n.Send()` through an interface was a dead `unresolved` edge with no link to the concrete impl |

## Phase-by-phase

### Phase 1 — critical: incremental reindex of a cross-file-referenced file
With `foreign_keys = ON`, `IndexFile`'s `DELETE FROM functions WHERE file_path=?`
violated the FK whenever another file's `calls.callee_id` still pointed into the
file, rolling back the transaction — and `RunWorker` discarded the error, so
edits to widely-called files never reached the index.

Fix: before deleting the file's functions, null + mark `'stale'` every
cross-file edge pointing into it, then re-resolve those edges against the rebuilt
resolver in the same transaction (re-parsing the affected caller files to recover
receiver context the `calls` table doesn't store). Edges whose target was renamed
or deleted become `unresolved` — never a dangling id. Deleted files are now
handled as a removal instead of erroring on the missing read.
Tests: callee-file reindex, callee rename, callee delete.

### Phase 2 — surface index health instead of swallowing errors
`RunWorker` now captures `Index`/`IndexFile` errors, logs to stderr, and records
per-file failures in a new `index_errors` table (migration v7); successes clear
the relevant errors and stamp `last_full_index` / `last_incremental_index` in
`_meta`. A `staleness` object `{last_full_index, last_incremental_update,
failed_files, healthy}` plus a one-line warning is attached to every MCP tool
response (JSON tools embed it; `get_architecture` appends an inline warning).
`--status` reads the same health and lists failed files.
Tests: migration v7, worker records a failed index, query_codebase carries
staleness under failure.

### Phase 3 — FTS5 query sanitization + correct error classification
`query_codebase` tries the raw query first (power users keep FTS syntax) and, on
a malformed-MATCH error, retries with a sanitized form that quotes each token and
prefix-matches the last (`db.Open(path` → `"db" "Open" "path"*`). A query that
sanitizes to nothing returns an empty set, never an error. Genuinely malformed
queries map to a new distinct `CodeQuerySyntax` (simplify the query), never
`CodeIndexBusy`; busy/lock errors stay classified as transient. The FTS5
"no such column" form (from a hyphenated query like `retry-logic`) is recognized
as a query error, not a schema error.
Tests: agent queries never error, sanitizer cases, syntax-vs-busy classification.

### Phase 4 — resolver performance on incremental updates
The resolver now supports delta maintenance: `removeFile` replays per-file
provenance to undo a file's contributions and `addFileFromDB` re-adds them from
that file's rows only. The worker holds one `ResolverCache` across incremental
updates (invalidated on full reindex and on any error that could desync it).
Conflict-tracked field/global type maps became refcounted distinct-value sets so
removal is exact.

Result — single-file reindex latency is now **flat across repo size**:

| Repo size | Before (full rebuild) | After (delta cache) |
|---|---|---|
| 200 files / 5k funcs | 35 ms | 15.0 ms |
| 2000 files / 50k funcs | 224 ms | 15.3 ms |

≈15× faster at 50k functions and independent of repo size (the Phase 4 target).
Full-index semantics are unchanged (existing resolution/golden tests) and an
end-to-end equivalence test proves the cached incremental call graph matches a
full reindex across adds, edits, cross-file renames, and deletes.

### Phase 5 — interface dispatch as low-confidence impact edges
The resolver parses each interface's method set and, at index time, fans out
interface-method calls (`n.Send()` where `n` is statically an interface) to every
concrete type whose method set satisfies the interface — emitting synthetic
`resolution='interface-dispatch'` call edges (the plan's second storage option;
no schema change). Matching is name-based, so the edges are explicitly low
confidence. `get_impact` excludes them by default (blast radius unchanged) and
includes them only at a low `min_confidence` (new `interface-dispatch` level),
labeling them `via_resolution`. The implements relation is maintained in the
delta resolver, so incremental and full reindex produce identical interface edges.
Tests: the Notifier/Email/SMS fixture (included at low confidence, excluded at
default/high) and an interface-dispatch equivalence test.

### Phase 6 — honest language matrix + housekeeping
`docs/LANGUAGES.md`, derived from the tree-sitter queries, parser dispatch, and
resolver strategies, states parsed-vs-resolved per language — making clear only
Go, TypeScript/TSX, and Python have a real scope resolver; others fall back to
`name-global`, and Swift extracts no call sites. Linked from the README. `setup`
now idempotently adds `.max-context/` to the project `.gitignore`.

## Design decisions worth calling out

- **Phase 1 re-resolution re-parses caller files** rather than replaying stored
  columns, because the `calls` table doesn't persist receiver kind/type. This is
  correct (full call-site context) and cheap (only files that referenced the
  changed file).
- **Phase 4 chose delta maintenance over scoping the resolver's loads.** Scoping
  to "the changed file's package + importers" risked silently dropping
  cross-package links (changing semantics); a delta resolver guarded by an
  end-to-end equivalence test keeps semantics provably identical to a full scan.
- **Phase 5 used synthetic edges, not an `implements` table**, so the existing
  recursive-CTE traversal in `get_impact`/`get_call_chain` picks up interface
  fan-out for free, gated purely by `min_confidence`.

## Remaining known gaps

- **Cross-package / monorepo resolution.** `cross-package` linking is single-
  module and matches an imported package by its path's last segment; multi-module
  monorepos and package-name ≠ path-segment cases fall back to
  `import-qualified` (unlinked).
- **Generic-tier language resolution.** JS/JSX, Rust, Java, C, C++, Ruby resolve
  calls by global name only (no receiver/type/inheritance); Swift extracts no
  call sites. Deeper resolution for these is unimplemented.
- **Interface dispatch is Go-only and name-only.** Detected via a Go `interface`
  type definition; method matching ignores arity and parameter types, so it can
  over-approximate (hence low-confidence, opt-in). TypeScript/Python interfaces
  are not yet fanned out.
- **Interface-dispatch precision.** No structural/embedded-interface handling;
  embedded interface methods are not expanded.
- **Embedding / semantic search.** Search is lexical (FTS5 BM25 + sanitized
  tokens); there is no embedding-based semantic retrieval.

## Follow-up

`get_call_chain` now gates interface-dispatch edges behind `min_confidence`
exactly like `get_impact`: a new `min_confidence` parameter (same enum, including
`interface-dispatch`) excludes the low-confidence interface fan-out by default and
includes it only when set low. Covered by the interface fixture test, which
asserts the interface caller appears in `get_call_chain` callers only at low
confidence.
