# max-context vs grep — Causal A/B Findings

*Updated 2026-05-31 with the v2 transitive-reachability run (13 tasks, adds C04/C05
deep-chain caller tasks). Current binary (all tool fixes incl. module-constant
indexing + inherited-method resolution), clean results. Earlier exploratory runs
are archived in `FINDINGS-archive-*.md`; the prior 11-task run is `results/httpx-run-1/`,
this one is `results/httpx-v2-run-1/`.*

**Question:** Does an LLM understand and edit code *better* with max-context's
MCP tools than with grep + read_file?

---

## Method

- **Two arms, one variable.** Same task model (Claude Sonnet 4.6), same prompt
  skeleton, same 15-turn budget, temperature 0. Only the toolset differs:
  - **grep arm** — real ripgrep + `read_file` (ranges/whole) + `list_files`,
    choosing its own search terms (not a strawman).
  - **max-context arm** — the real tools over **stdio MCP** to the actual
    `max-context` binary (`get_definition`, `query_codebase`, `get_call_chain`,
    `get_impact`, `get_architecture`).
- **Grading (objective-first).** Retrieval ("where is X?") → file-set F1 + a
  lead-file check, vs a hand-verified key from an **independent** oracle
  (ripgrep + manual; never max-context's own output). Relationship ("all callers
  of X") → symbol recall vs a hand-verified caller set. Edit ("where/how to
  change X") → a **different** model (Claude Haiku 4.5) scoring a pre-registered
  rubric (no self-judging). Infra failures excluded from quality, shown separately.
- **Backend:** AWS Bedrock. **Pre-registration hash (v2):** `b5a83409…` (in the manifest).
- **Scope:** 1 repo (httpx @ `b5addb64`), **13 tasks**, 1 replicate. **Small n** —
  this is a direction + effect-size result on a defined sample, not a universal law.

### The v2 hypothesis — and its honest refutation

v2 was designed to convert the quality *tie* into a quality *win*. The theory:
transitive ("blast-radius / everything that can *reach* X") caller queries should
break grep's **recall**, because grep does one textual hop per search and the deep
public callers (`get`/`post`/`head`/…) share no text with the deep target — so under
a turn budget the agent should drop them. Two new tasks (C04: 5-hop chain to
`_send_single_request`; C05: 4-hop chain to `_merge_url`), both with hand-verified
keys (ripgrep + manual enclosing-function, independent of max-context).

**The hypothesis did not hold.** Given a playbook that explicitly told it to trace
callers level-by-level, the grep agent (Sonnet 4.6) achieved **full recall (1.00)
on both C04 and C05** — it brute-forced every hop. So this is an **honest negative
on the quality axis**: at this model/repo, a capable grep agent is *not* less
accurate on deep-chain tasks. What it *is*, is far more expensive (next section).

### v4 — the FIRST quality win: aliased-import recall (`results/alias-v4/`)

v2's null pointed at the one place grep's *recall* should structurally break:
**renamed imports.** `from m import f as g`, then `g(...)` — the call site shares no
text with `f`, so grepping the original name cannot find it. After building cross-file
import resolution (v3) including aliases, two tasks tested it:

- **A01 (controlled repo `payments`):** `post_transaction` defined once, called via
  **two different aliases** (`apply_entry`, `record_payment`) at **5 sites in 2 files**.
  Hand-verified key = the 5 enclosing functions.
  - **grep: recall 0.00.** It read the `import … as …` lines, yet concluded
    *"`post_transaction` does not exist in this codebase — zero callers."* It cannot
    connect `apply_entry(...)` back to `post_transaction`.
  - **max-context: recall 1.00 in 2 calls** (`import-symbol` edges).
  - **This is the first paired quality win of the series** (Δ +1.00; McNemar mc-only).
- **A02 (real repo `celery`):** the one genuinely-unique internal alias found across
  16 repos surveyed — `from .annotations import resolve_all as resolve_all_annotations`.
  - **Tie (both 1.00)** — but grep paid **13 calls / 33,614 tokens** (it read `task.py`,
    spotted the alias line, then grepped the *alias* name) vs max-context's **2 calls /
    4,477 tokens (−87%)**. grep *can* unwind one alias hop if it reads the right file;
    it just pays heavily and the recovery is fragile.

**Honest reading.** The alias recall-win is **real and mechanism-proven** (A01 is
unambiguous: grep 0, mc 5/5). But it is **rare in the wild**: a survey of 16 popular
Python/TS repos found internal hot-function aliasing almost always either (a) keeps the
original name at call sites (module aliases like `import exc as sa_exc` → `sa_exc.Foo`),
(b) has a single call site, or (c) aliases a non-`def` callable. Only celery's
`resolve_all` qualified, with one caller. So: **where it occurs, max-context strictly
beats grep on recall; it just doesn't occur often.** A01 is a constructed existence
proof; A02 is the real-world echo (tie on quality, large cost win).

---

## TL;DR

- **Quality: a tie on every *naturally-occurring* task; the ONE structural win is
  aliased imports.** On the 13 httpx tasks (incl. v2's deep transitive chains) Cliff's
  δ = 0.00 — max-context is *as accurate as* a capable grep agent, not better. The
  **single exception** is renamed-import recall (v4): when a hot function is called
  through an import alias (`f as g`; call site reads `g(...)`), grep's recall collapses
  — on the controlled A01 it scored **0.00** ("function does not exist") while
  max-context got **5/5**. That's the first and only paired quality win, and it's
  **rare in real code** (1 qualifying case in 16 repos surveyed). Honest summary:
  **equal quality everywhere except aliased imports, where max-context strictly wins.**
- **Cost & speed: the edge widens with chain depth, decisively.** Across all 5
  relationship tasks max-context used **90% fewer tokens** (335,911 → 33,643). The
  deeper the chain, the bigger the gap: the two **transitive** tasks cost grep
  **237,457 tokens** vs max-context's **15,366 (−93.5%)** — because grep must brute-
  force every hop (C04: 21 tool calls, 132k tokens, 70s) while the recursive call
  graph returns the whole tree in **2 calls** (7,898 tokens, 25.6s).
- **The prior "edit-task blemish" is fixed at its root.** E02 (timeout-default
  task) previously *truncated with no answer*; it now completes 5/5 **and** uses
  fewer tokens than grep (23,952 vs 41,783). Cause was an **indexing gap**: the
  task's central symbol `DEFAULT_TIMEOUT_CONFIG` is a module-level constant the
  indexer didn't capture, so it was unsearchable and the model thrashed. Fix:
  index module/package-level constants (Python/TS/Go) as searchable symbols.
- **Overall:** median **−3,276 tokens/task** and **−2.7s/task** in max-context's
  favor. The win is **efficiency at equal quality**, concentrated on graph tasks
  and growing with chain depth.

---

## Results (n=13; 0 infra failures)

### Quality — tied everywhere, including the hardest tasks

| | value |
|---|---|
| Quality delta (mc − grep), median | **+0.00**, 95% CI [+0.00, +0.00] |
| Cliff's delta | +0.00 |
| McNemar (fully-correct discordant) | mc-only 0, grep-only 0 |

All 5 retrieval tasks, all 5 relationship tasks (incl. the v2 transitive C04/C05),
and all 3 edit tasks tied — every paired Δ = 0.00. The grep arm, told to trace
callers level-by-level, reached full recall even on the 5-hop chain. **Quality is a
tie; "strictly better quality" remains unproven** (and was actively tested against
in v2 — see the refuted hypothesis).

### Efficiency — where the real edge is (grep / max-context per task)

| Task | type | tool calls | tokens | wall-clock |
|---|---|---|---|---|
| **C04** transitive callers of _send_single_request (5-hop) | impact | **21 / 2** | 132,093 / **7,898** (−94%) | 70.1 / 25.6s |
| **C05** transitive callers of _merge_url (4-hop) | impact | **14 / 2** | 105,364 / **7,468** (−93%) | 61.6 / 14.9s |
| **C01** all callers of build_request | impact | 16 / **2** | 76,138 / **5,076** (−93%) | 51.7 / 10.8s |
| **C03** callers of _merge_url | impact | 4 / **2** | 12,281 / **5,392** (−56%) | — |
| **C02** callers of _build_auth_header | impact | 2 / 2 | 10,035 / **7,809** (−22%) | — |
| R01 where is URL | retrieval | 1 / 1 | 3,183 / 3,903 | — |
| R03 digest auth | retrieval | 2 / 3 | 5,049 / 6,235 | — |
| R04 build_request locus | retrieval | 6 / 6 | 10,811 / 13,635 | — |
| R05 Timeout config | retrieval | 4 / **1** | 7,237 / **3,961** | — |
| **E02** change timeout default | edit | 5 / 12 | 41,537 / **26,485** | — |
| E01 add a default header | edit | **4 / 18** | **12,018** / 50,308 | — |
| G01 why trust_env (grep-win) | edit | 20 / 19 | 107,183 / **59,499** | — |

(All tasks: quality Δ = 0.00; both arms completed; 0 infra failures this run.)

**Read this honestly:**
- **Relationship tasks (C01–C05): the cost win is large and scales with depth.**
  Summed over all 5, max-context used **90% fewer tokens** (335,911 → 33,643). The
  two transitive tasks are the extreme: grep brute-forced every hop (C04: 21 calls,
  132k tokens) for the *same* answer the recursive call graph gave in 2 calls (7.9k).
  This is the strongest cost result in the series, and it is on the *hardest* tasks.
- **Retrieval: even-to-favorable** — both correct; max-context modestly cheaper on
  R05; grep slightly cheaper on R01/R03/R04. A wash on cost, a tie on quality.
- **Edit tasks: E01's cost blowup was an INFRASTRUCTURE FLAKE, not edit-task noise
  (corrected diagnosis — see "Validated diagnoses" below).** E02 again beat grep
  (26,485 vs 41,537). E01 cost max-context 18 calls / 50,308 tokens at the same 5/5
  quality — but reading the transcript shows **7 of those calls failed** with
  `query_codebase → "index not ready"`: the background reindex worker rebuilt the
  FTS table mid-session, and a transient query error was mislabeled as a permanent
  "run /index-codebase first," so the agent looped retrying. My earlier "edit-task
  exploration noise" claim was **wrong**; the real cause is a concurrency/diagnostics
  bug, now fixed (transient FTS failures return a distinct "rebuilding, retry" code).
  So the E01 token figure is partly a harness artifact — not a clean edit-task signal.

---

## v3 addendum — cross-file import resolution (a latent loss, now fixed)

Chasing the alias hypothesis from v2's next-steps, verification surfaced something
bigger and unflattering: **max-context could not resolve ANY cross-file Python call.**
The bare-call resolver matched same-scope only (file = scope for Python); the httpx
C01–C05 wins all happened to be *within* `_client.py`. On a cross-file utility —
`to_bytes`, defined in `_utils.py`, called across `_auth.py`/`_multipart.py` —
`get_call_chain` returned **0 callers**. grep finds them all. That was a **silent
loss** hiding in the prior "tie."

**Fix (deterministic, no LLM, no false edges):** Python `from m import f [as g]`
now records the imported local name → original symbol; a bare call to that name
links cross-file to the origin **only when the origin has exactly one top-level
definition** (ambiguous → stays `unresolved`). On real httpx: `to_bytes` went
**0 → 11 cross-file callers**, all spot-checked correct (49 `import-symbol` edges
total, 0 false). This also resolves *aliased* calls (`g()` → `f`), the recall case
grep cannot text-match — mechanism-verified by a synthetic test
(`TestPythonCrossFileImportResolution`), but httpx's only aliases point to the
stdlib, so the **alias quality-win can't be A/B'd here** (origin not in index).

**v3 A/B result (1 task, `results/httpx-v3-crossfile/`):** task C06, "all callers
of `to_bytes`." Both arms now hit recall **1.00** (grep can still text-match the
name, so its recall holds) — but max-context used **3 calls / 8,490 tokens / 16.9s**
vs grep's **14 calls / 86,423 tokens / 50.9s (−90% tokens)**. So the honest effect
on httpx is **latent-loss → cost win at quality parity**, not a quality win.

**Now extended to all three languages.** The same cross-file mechanism was added to
TypeScript (`import { f as g }` named imports) and Go (cross-*package* `pkg.Func()`,
incl. aliased package imports `import u "x/util"`). Each is deterministic, gated on a
**unique** target so there are **no false edges** (TS/Python: unique top-level name;
Go: unique function in the package matching the import-path's last segment, else stays
`import-qualified`). Verified by synthetic probes + regression tests
(`TestTypeScriptCrossFileImportResolution`, `TestGoCrossPackageResolution`) and on the
self-repo (the golden test's `setup.Run`/`bench.Run` now correctly resolve
`cross-package` to their own packages, with the collision-mislink verified absent).
**Caveat (honest):** Go's package-name = path-segment heuristic *under-links* when two
imported packages share a segment (refuses rather than mislinks) — a false negative,
never a false positive. A module-graph-aware version would resolve those too.

---

## Validated diagnoses (deeper audit of the cross-file work)

The TS/Go resolution above was initially validated only on synthetic repos. A
real-code audit (same rigor as the Python/httpx 49-edge check) found and fixed
**three issues my earlier claims had missed** — each now backed by a measurement
and a regression test, not an assertion.

1. **False-positive bug: stdlib/3rd-party imports linked to same-named local
   functions (FOUND & FIXED).** On vitest, **38/1196 TS edges (3.2%)** linked a name
   imported from `node:fs`/`node:path` to a local function of the same name; on
   httpx, **4 edges** linked `unquote` (imported from `urllib.parse`) to httpx's own
   `_utils.unquote`. So the prior "49 edges, all correct" claim was **wrong** — it
   only spot-checked relative imports. **Fix:** seed a cross-file edge only when the
   import specifier is relative (`./`, `from .mod`) or its root matches a repo
   package (gated against the set of indexed top-level dirs/modules). Re-audit:
   httpx 49→45 edges (4 FPs gone, true positives kept), vitest 1196→755 (all
   relative, 0 node-builtin FPs). Tests: `TestExternalImportNotLinked`.
2. **Go cross-package: clean.** Audited all **103** `cross-package` edges on the
   self-repo — **103/103 correct, 0 false positives**. Of 784 `import-qualified`
   (unlinked) calls, **0 were genuine misses**: all are stdlib (`fmt`, `os`, …) or
   correctly-declined (a `db` *local var*, a `watcher.Start` *method* — linking
   either would be a false edge). The under-link caveat is real in principle but
   did not fire wrongly here.
3. **Module-constant caveat was understated (FOUND & FIXED).** I'd noted constants
   "inside conditionals are missed" as a minor caveat. It's a **common** pattern
   (optional-import `try/except`, version `if/else` gates) and was fully missed:
   `JSON_BACKEND` in a `try` and `FEATURE_FLAG` in an `if` both indexed as 0.
   **Fix:** anchor `@const.name` at module-level `try/except/if/else` blocks too,
   while still excluding function/class bodies (verified: function-scoped
   `LOCAL_IN_TRY` stays uncaptured). Tests: `TestModuleConstants/python-guarded`.

**E01 edit-task regression — corrected root cause + elegant fix.** Reading the actual
v2 transcript: 7 of the 18 calls were `query_codebase` failing with `"index not ready:
run /index-codebase first"` while the **background reindex worker rebuilt the FTS table
mid-session**. The old flow committed the data transaction, then did a *separate*
post-commit full FTS `'rebuild'` — leaving a window where a concurrent query saw an
empty FTS index, mislabeled as a permanent error, so the agent looped.
(`get_definition`/`get_call_chain` don't touch FTS, so they kept working — which is why
it still answered 5/5.) The earlier "edit-task exploration noise" label was a guess and
is **retracted**.

**Fix — root cause, not mask (schema V6):** the FTS5 tables are external-content, so
they're now kept in sync by `AFTER INSERT/UPDATE/DELETE` triggers *inside the same
transaction* as the data change — no separate post-commit rebuild, no empty window, and
incremental edits cost O(changed rows) not O(repo). Validated end-to-end: the watcher's
per-file `IndexFile` hot path shows **0 empty windows across 14,000 concurrent reads**
during 50 reindexes (`TestIncrementalReindexNoFTSWindow`), and the agent-facing
`query_codebase` path shows **2,950 OK / 0 busy / 0 "not ready" across 30 concurrent
full reindexes** (the JOIN to the snapshot-isolated `functions` table anchors a coherent
result). A `CodeIndexBusy` code remains as an honest belt-and-suspenders for any residual
transient (returns "rebuilding; retry / use get_definition" — never the misleading
"reindex"). Tests: `TestMigrationV6FTSTriggers`, `TestIncrementalReindexNoFTSWindow`,
`TestQueryCodebase_TransientFTSFailureIsBusy`.

---

## Two index-completeness fixes that drove these results

Both wins trace to making the index **complete** — when the right symbol/edge is
present, the model answers in ~2 calls; when it's missing, the model thrashes.

1. **Inherited method calls.** `self.build_request()` in `Client.request` calls
   a method defined on the parent `BaseClient`. The resolver now captures class
   base-classes (`class_bases`) and walks the inheritance chain, so
   `self.<inherited>()` links and `get_call_chain` returns the full caller tree
   in one call. (Was the C01 fix: 16+ calls → 2.)
2. **Module-level constants.** `DEFAULT_TIMEOUT_CONFIG = Timeout(timeout=5.0)` is
   a module-level assignment the indexer didn't capture, so it was unsearchable —
   and E02 ("change the timeout default") spiraled looking for it. The indexer now
   captures module/package-level constants (Python `NAME = …`, TS `export const`,
   Go `const`/`var`) as searchable symbols in `get_definition` + `query_codebase`.
   (Was the E02 fix: truncated → completed 5/5, cheaper than grep.)

The pattern in both: a **diagnosed completeness gap**, not a prompting problem.
Each was proven by injecting the missing symbol/edge and re-running before
building the real fix.

---

## Honest caveats

- **n is small** (13 tasks, 1 repo, 1 replicate). Quality deltas are not
  statistically significant; the credible claims are the **large within-task
  efficiency deltas** on relationship tasks and the qualitative tie on quality.
- **Quality is a tie on natural tasks; the lone structural win (aliased imports) is
  real but rare.** v2 disproved the deep-chain recall theory (grep brute-forces hops
  to full recall). v4 then *found* the one place grep's recall truly collapses —
  renamed imports (A01: grep 0.00, mc 1.00) — but a 16-repo survey shows internal
  hot-function aliasing is uncommon (1 qualifying real case, celery, single caller →
  a tie). So "strictly better quality" holds **only** for aliased-import recall, and
  that pattern is infrequent. Still-untested recall angles: dynamic / string-keyed
  dispatch (likely a *shared* blind spot, not a win) and very high cross-file fan-in.
- **Edit-task behavior is run-to-run noisy — shown in BOTH directions now.** E02
  beat grep again; E01 *regressed* (max-context over-explored to 4× grep's cost at
  equal quality). Same prompt class, opposite outcome from the prior run. A single
  replicate can swing hard here; more replicates needed before any edit-task claim.
- **Resolution remains partial by design:** normal inheritance + module constants
  resolve; calls on unrelated classes, locals from returns, deeper MRO chains, and
  dynamic dispatch stay `unresolved` (no false edges). Module-const capture is
  scope-anchored (top-level only); constants defined inside conditionals are missed.
- **One model.** Behavior is a model×tool-design interaction.

---

## Harness integrity

Every run ends with an explicit status; nothing is dropped silently. Guards in
place from earlier debugging: explicit `--rg` path + preflight (catches a broken
grep arm), per-task checkpointing + exact `Retry-After` (rate limits),
infra-failure exclusion from quality, and lead-file grading (avoids penalizing
correct answers that dismiss extra files). The grep arm chooses its own search
terms; the judge is a different model than the one under test.

---

## Bottom line

On httpx, **max-context matches grep's answer quality on every one of 13 tasks and
beats it on cost for call-graph / "what-calls-this" work — by 90% summed over the
relationship tasks, and the gap *grows with chain depth* (transitive tasks: −93.5%,
grep 237k vs mc 15k tokens).** Text search can be made exhaustive, but it pays for
exhaustiveness linearly in hops; the recursive call graph does not.

The honest, evidence-backed claim across v1–v4: **equal answer quality on every
naturally-occurring task, materially cheaper — most so on relationship/graph work
(up to 94% fewer tokens, ≈90% in aggregate on the v2 run) — and *strictly better* on
the one structural blind spot grep has: aliased imports** (A01: grep 0.00 vs mc 1.00). That last win is real but
infrequent in practice (1 qualifying case in 16 repos). max-context's value is
**efficiency at parity, plus correctness insurance against the cases text search
structurally cannot follow** (renamed imports today; the engine now resolves them
across Python, TS, and Go).

## Next steps (re-targeted by v4's result)
v4 confirmed the alias recall-win exists but is rare. Remaining angles:
1. **Cross-file fan-in at scale** — a hot util called in many files (now resolved
   cross-file in all 3 langs): test whether grep's recall holds under a turn budget
   when callers are scattered across 10+ files, not just 2–3.
2. **Dynamic / string-keyed dispatch** — registries, `getattr`, decorators. No static
   edge for grep AND none for max-context → likely a *shared* boundary, worth mapping
   honestly rather than as a win.
3. **More repos + replicates** — cross-repo n and edit-task variance (E01/E02 swung
   opposite ways across runs).
4. A **freshness test** (edit a file mid-session) — a correctness-over-time edge a
   static grep baseline structurally cannot win.

> Engine note: cross-file/cross-package call resolution now spans **Python, TS, and
> Go** (was silently broken for Python/TS before v3; Go cross-package was unlinked).
> Any repo whose call graph spans modules benefits — the early httpx C-task wins were
> same-file-only. All gated on a unique target → no false edges (Go under-links on
> ambiguous path segments rather than mislinking).

## Reproduce
See `README.md`. Canonical runs: `results/httpx-v2-run-1/` (13 tasks),
`results/httpx-v3-crossfile/` (cross-file), `results/alias-v4/` (the alias quality
win + real-repo tie). Each holds `results.json`, `report.md`, and full transcripts.
