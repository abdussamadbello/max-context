# max-context — Pipeline Architecture

How a file change becomes queryable knowledge, and how an AI agent reads it back.

max-context is two pipelines that meet at a single SQLite database:

- **Write pipeline** (deterministic, no LLM): watcher → parse → resolve → store.
- **Read pipeline** (agent-driven): MCP tool call → SQL/recursive CTE → result policy → JSON verdict.

The database is the seam. That separation is why the project can claim both *"always current"* (the write side runs continuously off OS file events) and *"no token spend at index time"* (the write side never calls a model — the host CLI's model does all reasoning on the read side).

---

## Full pipeline

```mermaid
flowchart TB
    subgraph WRITE["WRITE PIPELINE · deterministic · no LLM"]
        direction TB
        CB["Codebase<br/>*.go *.ts *.py …"]
        GIT["git checkout / rebase"]

        subgraph WATCH["Watcher · internal/watcher"]
            direction TB
            FSN["fsnotify events → handle()"]
            DEB["debounce 500ms / file"]
            GEV["HandleGitEvent · git.go<br/>git diff HEAD@{1} HEAD"]
        end

        WORKER{"Worker · RunWorker<br/>channel msg?"}

        subgraph IDX["Indexer · internal/indexer"]
            direction TB
            FULL["Index() — FULL<br/>scan all · truncate · reinsert all"]
            INCR["IndexFile(path) — INCREMENTAL<br/>delete+reinsert one file"]
            PARSE["ParseFile() · parser.go<br/>tree-sitter → language dispatch"]
            RESOLVE["ResolveCall() · resolver.go<br/>ReceiverKind decision tree<br/>→ (callee_id, confidence)"]
        end

        DB[("SQLite · internal/db<br/>functions · calls · types · imports<br/>struct_fields · package_vars · class_bases<br/>FTS5: functions_fts · types_fts")]
    end

    subgraph READ["READ PIPELINE · agent-driven"]
        direction TB
        AGENT["AI CLI / IDE<br/>Claude Code · Cursor · Copilot …"]
        SERVER["MCP Server · server.go<br/>stdin JSON-RPC → handleMethod"]
        ROUTER["Router · router.go<br/>tools/call → Handler.Call()"]

        subgraph TOOLS["5 MCP Tools · internal/tools"]
            direction TB
            T1["get_definition<br/>exact name → priority sort"]
            T2["query_codebase<br/>FTS5 + BM25 → loop guard"]
            T3["get_call_chain<br/>recursive CTE up/down"]
            T4["get_impact<br/>seed → CTE walk + confidence"]
            T5["get_architecture<br/>read summary/architecture.md"]
        end

        POLICY["Result Policy · result_policy.go<br/>definitive / ambiguous / partial<br/>+ recommended_next_action"]
    end

    CB -->|fs event| FSN
    GIT --> GEV
    FSN --> DEB
    DEB -->|relPath| WORKER
    GEV -->|"< N files: per-file path"| WORKER
    GEV -->|"≥ N files: write .reindex-queue"| FSN
    FSN -->|".reindex-queue touched → ''"| WORKER

    WORKER -->|"'' (sentinel)"| FULL
    WORKER -->|"path"| INCR
    FULL --> PARSE
    INCR --> PARSE
    PARSE --> RESOLVE
    RESOLVE --> DB

    DB -.->|same DB| TOOLS
    AGENT <-->|stdio| SERVER
    SERVER --> ROUTER
    ROUTER --> T1 & T2 & T3 & T4 & T5
    T1 & T2 & T3 & T4 --> POLICY
    POLICY -->|"JSON: results + answer_status + next_action"| AGENT
    T5 --> AGENT
```

---

## 1. Watcher — deciding *what* and *how* to reindex

`internal/watcher/watcher.go`, `internal/watcher/git.go`

The watcher converts raw OS events and git operations into exactly one of two signals on the worker channel: a **relative path** (reindex one file) or the **empty string** (reindex everything). The `.reindex-queue` sentinel file is the convergence point — both git bulk-changes and an external `/reindex` command trigger a full rebuild by writing that one file, which the watcher already watches.

```mermaid
flowchart TD
    EV["fsnotify event"] --> H["handle(event)"]
    H --> IGN{"base name<br/>in ignore list?"}
    IGN -->|yes| DROP["drop"]
    IGN -->|no| NEWDIR{"new directory<br/>created?"}
    NEWDIR -->|yes| ADD["recursively add<br/>to watch set"]
    NEWDIR -->|no| QUEUE{".max-context/<br/>.reindex-queue<br/>touched?"}
    QUEUE -->|yes| DELQ["delete sentinel"] --> FULLSIG["send '' → FULL reindex"]
    QUEUE -->|no| EXT{"supported<br/>extension?"}
    EXT -->|no| DROP2["drop"]
    EXT -->|yes| DEB["debounce(relPath)<br/>500ms timer per file<br/>(reset if already pending)"]
    DEB -->|"timer fires"| INCRSIG["send relPath → INCREMENTAL"]

    G["git HEAD/index change"] --> HGE["HandleGitEvent()"]
    HGE --> DIFF["git diff --name-only<br/>HEAD@{1} HEAD"]
    DIFF --> CNT{"changed files<br/>vs maxIncremental"}
    CNT -->|"< N"| PER["send each path<br/>(many incrementals)"] --> INCRSIG
    CNT -->|"≥ N"| WQ["write all paths to<br/>.max-context/.reindex-queue"] --> QUEUE
```

**Key behaviors**

- `debounceMs = 500` — rapid saves to the same file collapse into one reindex; the pending-timer map is mutex-protected.
- A fresh clone with no reflog makes `git diff HEAD@{1}` fail; `GitChangedFiles` returns `nil` and is ignored rather than erroring.
- Directory creation is handled live: new sub-trees are added to the watch set recursively, so newly-created folders are covered without a restart.

---

## 2. Worker & Indexer — full vs incremental

`internal/indexer/indexer.go`

`RunWorker` is a single consumer on the channel. The message *is* the branch: `""` → full rebuild (and write status artifacts), any path → incremental.

```mermaid
flowchart TB
    MSG{"RunWorker<br/>channel message"}
    MSG -->|"'' (empty)"| FULL["Index()"]
    MSG -->|"relPath"| INCR["IndexFile(path)"]

    subgraph F["FULL · Index()"]
        direction TB
        F1["Scan() all supported files"]
        F2["BEGIN txn"]
        F3["TRUNCATE every table<br/>calls · functions · types · imports<br/>struct_fields · package_vars · class_bases"]
        F4["parse + insert ALL files"]
        F5["build Resolver from full DB"]
        F6["resolve ALL calls"]
        F7["COMMIT (large) → rebuild FTS"]
        F1-->F2-->F3-->F4-->F5-->F6-->F7
    end

    subgraph I["INCREMENTAL · IndexFile(path)"]
        direction TB
        I1["read ONE file"]
        I2["BEGIN txn"]
        I3["DELETE rows WHERE file_path=?<br/>(calls before functions: FK order)"]
        I4["parse + insert THIS file only"]
        I5["build Resolver from FULL DB<br/>← cross-file links resolve here"]
        I6["resolve this file's calls"]
        I7["COMMIT (small) → rebuild FTS"]
        I1-->I2-->I3-->I4-->I5-->I6-->I7
    end

    FULL --> F1
    INCR --> I1
    F7 --> ART["write status.json /<br/>summary.md / architecture.md"]
```

**The subtle correctness point:** incremental indexing re-parses only one file, but **builds the resolver from the entire database** (`indexer.go:377`). A call in the edited file can therefore still resolve to a definition in an untouched file — cross-file accuracy without re-parsing the world. Deletes run in dependency order (calls before functions) so foreign keys never break mid-transaction.

---

## 3. Parser — tree-sitter, then language dispatch

`internal/indexer/parser.go`

Parsing is deterministic and language-gated. A shared tree-sitter pass produces a query tree; then `ParseFile` dispatches to a typed handler (Go/TS/Python) or falls back to `parseGeneric` for everything else. Only the typed handlers extract the receiver metadata that powers high-confidence call resolution.

```mermaid
flowchart TB
    IN["ParseFile(path, content)"] --> LANG{"language for<br/>extension?"}
    LANG -->|none| EMPTY["return empty result"]
    LANG -->|supported| TS["tree-sitter parse<br/>→ run language query"]
    TS --> DISP{"dispatch"}

    DISP -->|Go| GO["parseGo<br/>receiver classification<br/>+ 9a return-type<br/>+ 9b field/global types"]
    DISP -->|TS/TSX| TYPESCRIPT["parseTS<br/>annotations · this / this.field"]
    DISP -->|Python| PY["parsePython<br/>type hints · self / self.field"]
    DISP -->|other| GEN["parseGeneric<br/>bare calls only<br/>(no receivers, no 9a/9b)"]

    GO & TYPESCRIPT & PY & GEN --> OUT["ParseResult"]
    OUT --> O1["Functions<br/>kind · receiver_type · package · return_type"]
    OUT --> O2["Calls<br/>receiver_kind · receiver_type<br/>receiver_field · imported_origin"]
    OUT --> O3["Types · Imports · StructFields<br/>PackageVars · ClassBases · Consts"]
```

**What each typed handler captures**

- **Go** — package + imports (with aliases), funcs/methods with body spans, statically-typed params & locals, `x := f()` bindings (feeds *9a* return-type inference), `base.field.M()` field-receiver calls (feeds *9b*), consts (made searchable), struct fields.
- **TypeScript** — module/named imports with aliases, class bodies & methods, type-annotated params/locals/fields, `this.field.M()` and `this.M()` calls.
- **Python** — `from m import f [as g]` with origin mapping, class defs + base classes, type hints, `self.x = Ctor()` assignments, `self.field.M()` calls.
- **Generic** — legacy capture only; every edge is a bare call with no receiver, so 9a/9b are unavailable.

> *9a* = infer a receiver's type from the **return type** of the function that produced it.
> *9b* = infer a receiver's type from a **struct field** or **package-level variable** declaration.

---

## 4. Resolver — the ReceiverKind decision tree

`internal/indexer/resolver.go`

This is the heart of call-graph accuracy. The resolver first builds scope indexes (`byPkgName`, `byRecvName`, `byTopName`, plus `fieldType`/`globalType`/`bases` maps), then dispatches each call on the `ReceiverKind` the parser attached. Every outcome carries a **confidence marker** that is stored in `calls.resolution` and reused later as a query filter.

```mermaid
flowchart TB
    RC["ResolveCall(call, scope)"] --> STRAT{"language<br/>strategy"}
    STRAT -->|Go/TS/Py| TYPED["typedReceiverStrategy"]
    STRAT -->|JS/other| NAMEG["nameGlobalStrategy"]

    TYPED --> KIND{"ReceiverKind"}

    KIND -->|"import: pkg.Foo()"| IMP{"unique top-level<br/>in target pkg?"}
    IMP -->|yes| RCP["resCrossPackage"]
    IMP -->|no| RIQ["resImportQualified (unlinked)"]

    KIND -->|"var: typed x; x.M()"| VAR["methodOnType(type, M)"]
    KIND -->|"from-callee 9a: x:=f(); x.M()"| FC{"class? / has return_type?"}
    FC -->|yes| VAR
    FC -->|no| RU1["resUnresolved"]
    KIND -->|"field 9b: a.field.M()"| FLD["fieldType[(T,field)]<br/>→ methodOnType"]
    KIND -->|"maybe-global 9b: x.M()"| GLB["globalType[(pkg,x)]<br/>→ methodOnType"]

    VAR & FLD & GLB --> MOT{"methodOnType<br/>(BFS up inheritance chain)"}
    MOT -->|"unique match"| RRT["resReceiverTyped"]
    MOT -->|"none / ambiguous"| RU2["resUnresolved"]

    KIND -->|"bare call: foo()"| BARE{"classify"}
    BARE -->|builtin| RB["resBuiltin"]
    BARE -->|class/struct name| RCN["resConstructor"]
    BARE -->|1 same-scope match| RSF["resSameFile / resSamePackage"]
    BARE -->|1 imported origin| RIS["resImportSymbol"]
    BARE -->|else| RU3["resUnresolved"]
```

**Two "refuse to guess" stances built in:**

- `loadFieldTypes` / `loadGlobalTypes` **delete conflicted entries** — if a field or var name maps to more than one type across files, it's dropped rather than linked. An honest `resUnresolved` beats a wrong edge.
- `methodOnType` does a **breadth-first walk up `class_bases`**, so a method call resolves to a base-class definition when the subclass doesn't define it — but only on an *unambiguous* single match.

**Confidence markers** (stored in `calls.resolution`, highest trust first): `resSameFile` · `resSamePackage` · `resImportSymbol` · `resReceiverTyped` · `resCrossPackage` · `resImportQualified` · `resBuiltin` · `resConstructor` · `resNameGlobal` · `resUnresolved`.

---

## 5. Database — schema & FTS

`internal/db/schema.go`, `internal/db/queries.go`, `internal/db/fts.go`

A versioned migration system (v1–v5, idempotent via `_meta`) builds the relational core plus two FTS5 virtual tables that use *external content* — they index directly from the base tables and are rebuilt after every index pass.

```mermaid
erDiagram
    functions ||--o{ calls : "caller_id"
    functions ||--o{ calls : "callee_id"
    functions {
        int id PK
        string name
        string file_path
        int start_line
        int end_line
        string kind "func / method (v2)"
        string receiver_type "v2"
        string package "v2"
        string return_type "v3"
    }
    calls {
        int id PK
        int caller_id FK
        int callee_id FK "nullable: external"
        string callee_name
        string resolution "confidence marker (v2)"
        string receiver_name "v2"
        int line
    }
    types {
        int id PK
        string name
        string kind "class/struct/interface/type/constant"
        string definition
        bool exported
    }
    struct_fields {
        string struct_type
        string field_name
        string field_type "v4"
    }
    package_vars {
        string name
        string var_type "v4"
    }
    class_bases {
        string class_name
        string base_name "v5: inheritance"
    }
```

- `functions_fts(name, file_path, code, docstring)` and `types_fts(name, file_path, definition)` are FTS5 external-content tables, queried with `MATCH` + BM25 ranking + `snippet()`.
- Indexes on `calls.resolution`, `calls.caller_id`, `calls.callee_id` make the recursive call-graph walks and confidence filtering cheap.

---

## 6. MCP server & routing

`internal/mcp/server.go`, `router.go`, `handler.go`

A line-oriented JSON-RPC 2.0 loop over stdio. The router maps method names to handlers; tool panics are caught and converted to RPC errors so a single bad call can't crash the server.

```mermaid
sequenceDiagram
    participant A as AI CLI / IDE
    participant S as Server (server.go)
    participant R as Router (router.go)
    participant H as Handler (handler.go)
    participant T as Tool (internal/tools)

    A->>S: JSON-RPC line (stdin)
    S->>R: handleMethod(req)
    alt method = initialize
        R-->>A: protocol version + capabilities
    else method = tools/list
        R-->>A: 5 tool schemas
    else method = tools/call
        R->>H: Handler.Call(name, params)
        Note over H: read-lock handlers map<br/>recover() panics → RPC error
        H->>T: dispatch to tool
        T-->>H: result (ContentItem)
        H-->>R: interface{}
        R-->>A: JSON-RPC response (stdout)
    else method = resources/read
        R-->>A: .max-context/{summary,architecture}.md
    else unknown
        R-->>A: MethodNotFound error
    end
```

---

## 7. Tools & result policy — decisive answers

`internal/tools/*.go`, `internal/tools/result_policy.go`

Every tool returns more than rows: it returns a **verdict** (`answer_status`) and a **routing instruction** (`recommended_next_action`). That's what makes the surface "decisive" — it steers the agent's control flow instead of dumping data.

### get_definition & query_codebase — canonicalization

Both reuse the same priority rule (`type/class > function > method`). A single exact-name hit short-circuits to `definitive`, so fuzzy search degrades gracefully into exact lookup.

```mermaid
flowchart TB
    Q["get_definition / query_codebase"] --> N{"# exact-name hits"}
    N -->|0| PART["answer_status = partial<br/>next = call_query_codebase<br/>(or nearbyTerms suggestions)"]
    N -->|1| DEF["answer_status = definitive<br/>next = answer_now · canonical=true"]
    N -->|"≥ 2"| CANON{"unique best<br/>priority?"}
    CANON -->|yes| OVER["definitive + overloaded<br/>list secondary_matches"]
    CANON -->|"tie"| AMB["answer_status = ambiguous<br/>next = inspect_relevant_file"]

    Q -.->|query_codebase only| LG{"callCount<br/>this session"}
    LG -->|"≥ 4 (nudgeAfter)"| W["loop_guard = warning<br/>nudge: answer or switch tools"]
    LG -->|"≥ 6"| STOP["loop_guard = stop_searching<br/>next = switch_tools_or_answer"]
```

**The loop guard** is an anti-pattern detector aimed at the *agent*, not the code: `query_codebase` counts calls per session and, after `nudgeAfter = 4`, pushes the model to commit to an answer or switch to `get_impact`/`get_call_chain`; at `≥ 6` it escalates to `stop_searching`. `query_codebase` also normalizes `max_results` to `[1, 50]` (default 3) and runs a `nearbyTerms` prefix-LIKE fallback when FTS returns zero rows.

### get_call_chain & get_impact — recursive CTE walks

Both share one recursive-CTE shape and flip only the join direction. `get_impact` adds seed extraction, confidence filtering, and a hard node cap.

```mermaid
flowchart TB
    subgraph CC["get_call_chain"]
        direction TB
        CCIN["function_name · direction · depth (1–5)"]
        CCUP["callers (upstream):<br/>JOIN calls ON callee_id = chain.id<br/>→ caller_id"]
        CCDN["callees (downstream):<br/>JOIN calls ON caller_id = chain.id<br/>LEFT JOIN functions (keep externals)"]
        CCIN --> CCUP
        CCIN --> CCDN
    end

    subgraph IM["get_impact"]
        direction TB
        IMIN["files (or from_git=HEAD)"]
        SEED["SymbolsInFile → seed IDs"]
        FILT["edge filter:<br/>resolution IN markers ≥ min_confidence<br/>(same-file 5 · same-package 4 ·<br/>receiver-typed 3 · name-global 1)"]
        WALK["recursive CTE walk<br/>callersWalk / calleesWalk / both"]
        CAP["dedup · skip tests · LIMIT 1000<br/>(maxImpactNodes → truncated flag)"]
        STATS["stats: changed / impacted counts<br/>max_depth · resolution_breakdown"]
        IMIN --> SEED --> FILT --> WALK --> CAP --> STATS
    end
```

- **`get_call_chain`** clamps depth to `[1, 5]` (default 2); downstream uses `LEFT JOIN` so external/unresolved callees survive via `callee_name`.
- **`get_impact`** turns changed *files* into seed *symbols*, walks the graph, and reuses the `calls.resolution` markers as a `min_confidence` filter — the same confidence computed once at index time, spent again at query time. Results are capped at `maxImpactNodes = 1000` with a `truncated` flag, and test files are excluded unless `include_tests=true`.

### get_architecture

Reads pre-computed `.max-context/summary.md` and `architecture.md` and returns them concatenated — no query, just the artifacts written during the last full index.

---

## Cross-cutting design notes

- **Confidence is a first-class column, not a runtime heuristic.** Computed once during resolution, stored in `calls.resolution`, and reused as a query-time filter in `get_impact`. One concept, two payoffs.
- **One sentinel, many triggers.** Git bulk-changes and external `/reindex` both converge on writing `.max-context/.reindex-queue`, which the watcher already watches — a single "full rebuild" path.
- **The tools steer the agent.** `answer_status` + `recommended_next_action` + the loop guard are designed to shape the host model's behavior, turning a search API into a decision API.
- **Refuse to guess.** Conflicted field/global types are deleted; ambiguous matches return `ambiguous` rather than a wrong pick. The system prefers an honest "don't know" over a confident error.
