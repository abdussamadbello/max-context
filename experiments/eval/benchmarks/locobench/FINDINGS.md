# LoCoBench arm — findings

*Status: pipeline proven; first diagnostic run only. n=1 scenario. NOT a result —
a direction + a list of confounds to fix before any batch run.*

## Setup

- Scenario: `go_api_graphql_hard_007_feature_implementation_medium_01` (Go, GraphQL
  API, medium difficulty, ~264K-token project, 7 binary rubric criteria).
- Option A (repurpose): project indexed; both arms get only the task (discovery
  mode — LoCoBench's file-list recipe stripped); each gathers context itself.
- Grading: shared blind judge (Haiku 4.5) vs scenario's ground_truth +
  evaluation_criteria. Task model: Sonnet 4.6. Backend: Bedrock.

## The truncation diagnostic

v1 left the max-context arm truncated (63 calls, no answer). Hypothesis: a prompt
artifact — the MCP playbook said "prefer a targeted query," nudging per-symbol
lookups. Test: add an explicit STOPPING DISCIPLINE to the MCP playbook (one-sided,
audited divergence) + raise turns 25→40, rerun the SAME scenario.

| Run | arm | status | calls | in_tok | out_tok | score |
|-----|-----|--------|------|--------|---------|-------|
| v1  | grep | completed | 11 | 99,268 | 5,738 | **7/7** |
| v1  | max-context | truncated | 63 | 255,511 | 3,545 | — (no answer) |
| diag | grep | completed | 14 | 147,509 | 6,052 | **7/7** |
| diag | max-context | **completed** | 69 | **385,658** | 8,250 | **2/7** |

### What the stopping tweak did — and didn't

- **Did:** converted truncation → a completed answer. So truncation WAS partly a
  prompt/budget issue; the arm can finish.
- **Did NOT:** reduce exploration. Calls went 63→69; input tokens 255K→**386K**
  (still **2.6× grep**). `get_definition` dominated: **43 calls** (+ 20
  get_call_chain). The "don't look up every symbol" instruction did not stop the
  per-symbol lookup loop.

### Why the answer scored 2/7 — ROOT CAUSE FOUND (verified)

Judge: the MC answer nailed the repository + service layers WITH correct guard
clauses (c04, c05 = 1) but **omitted the GraphQL schema changes and the resolver
layer entirely** (c01, c02, c03 = 0) and **made out-of-scope edits** to REST
handlers + auth scopes (c07 = 0). grep got 7/7 on the same task.

**Root cause = unindexed file types, confirmed by reading the index DB.** The task
requires editing `api/graphql/storyboard.graphqls` and `mutation.graphqls`. Those
files exist on disk but were **never indexed**:

- Project on disk: 42 `.go`, **7 `.graphqls`**, 7 `.md`, 2 `.yaml`, 1 `.yml`,
  1 `.json`, 1 `.sh`, 1 `.mod`.
- Index DB (`.max-context/index.db`): `SELECT … LIKE '%graphql%'` → **NONE**.
  `functions.language` = `{go}` only. The indexer captured the Go code and nothing
  else.

**Mechanism (pkg/treesitter/bindings.go `LanguageForExt`):** max-context indexes
only tree-sitter-parseable CODE languages — TS/TSX/JS/JSX, Python, Go, Rust, Java,
C, C++, Ruby, Swift. `.graphqls` (GraphQL SDL) has no binding → `LanguageForExt`
returns false → the watcher/indexer skips it. Same for `.md`, `.yaml`, `.json`,
`.proto`, `.sql`, Dockerfiles — every non-code artifact. So **no max-context tool
could surface the schema files; they weren't in the index.** grep reads raw bytes
off disk and found them on the first keyword search.

This is NOT a prompt artifact and NOT mainly an over-exploration story — it is a
**coverage gap**. The agent built a complete picture of the half of the task that
lived in `.go` files and was blind to the half that lived in `.graphqls`.

## Reading (honest)

- **The 2.5–2.6× token cost is real on this task, but secondary.** Two playbooks,
  same over-exploration; symbol-anchored point queries cost more than grep's
  keyword-search-then-read-whole-file on a generation task. Worth noting, but the
  quality miss dominates.
- **The headline finding is structural and verified: max-context is blind to
  non-code files.** On any task touching schemas (GraphQL/SDL/proto), config
  (yaml/json/toml), SQL migrations, or docs, the index simply does not contain
  them, so the agent cannot retrieve them through MC tools. A text baseline (grep)
  is artifact-agnostic and does not have this gap. This is a real product signal:
  retrieval-completeness on mixed-artifact repos, not just call-graph efficiency.

## Confounds / scope

1. **One scenario, one replicate.** But the root cause (LanguageForExt skips
   `.graphqls`) is deterministic and code-verified, so it generalizes to ANY
   scenario whose required files are non-code types — that's checkable statically.
2. **One-sided playbook.** The stopping tweak is MC-only; fine for this diagnostic.
   It changed truncation→completion but not token cost, so it is not the cause.
3. **feature_implementation is generation-heavy** → plays to grep regardless.

## Blast radius — measured across all 8,000 scenarios (static, no LLM)

`cmd/coverage` (re-runnable; mirrors `pkg/treesitter/bindings.go`) classifies every
scenario by whether max-context could index its `context_files`:

- **Only 819 / 8,000 (10.2%) are "fully indexable"** — every required file is a
  parseable code language. The fair-fight subset is `fair-fight-subset.txt`.
- **Two distinct coverage gaps:**
  1. **Unsupported code languages — 1,600 scenarios (20%).** C# and PHP are 2 of
     LoCoBench's 10 languages; max-context has no tree-sitter binding for either,
     so it can't parse those repos at all. (Clean scope boundary; matches in-house
     language list.)
  2. **Non-code artifacts — 5,581 / 6,400 supported-language scenarios (87.2%).**
     Even on Go/TS/Py/… repos, the task's required files include something never
     indexed: `.md` (12,130), `.txt` (7,572), no-ext (4,317), `.yaml`/`.yml`
     (3,260), `.json` (1,922), `.xml` (1,476), `.sh` (887), `.html` (829),
     `.proto` (642), `.toml` (549), `.tf` (461), GraphQL SDL, SQL, …
- **By category, ≥80% of every category touches an unindexable file**
  (code_comprehension 100%, feature_implementation 96%, integration_testing 96%,
  architectural_understanding 93%).

So the go_api_graphql 2/7 was not a fluke — it is the *typical* case. On ~90% of
LoCoBench, max-context is structurally missing part of the required context.

(Loader note: a scenario id's embedded difficulty is the generation *target* and
can differ from the achieved `difficulty` field — `ProjectPrefix` parses the
prefix from the id's own tokens, matching difficulty against the known set, not by
substituting the field. 35 scenarios hit this; fixed.)

## Reading the gap (product vs. benchmark)

- This is a **retrieval-completeness** result, orthogonal to the in-house
  call-graph-efficiency wins. Both can be true: MC is cheaper on pure code-symbol
  graph queries AND blind to the non-code half of mixed-artifact tasks.
- It is a genuine **product signal**: a coding agent backed only by a code-symbol
  index cannot retrieve schemas/config/IDL/docs it needs to edit. A fix would be
  indexing non-code files for plain text/FTS search (no parsing needed) — turning
  ~90% "partially blind" into "covered."

## Fair-fight subset — batch1 complete (6 scenarios, 12 runs)

Matched runs on fully-indexable scenarios (all files parseable code), fair
(mirrored) playbooks, 30 turns, Sonnet 4.6 task / Haiku 4.5 judge, Bedrock.

| scenario (Go/Py, gateway/graphql) | category | arm | calls | in_tok | score |
|---|---|---|------|--------|-------|
| go_api_gateway_081 | security | grep | 58 | 1,506,528 | 3/8 |
| go_api_gateway_081 | security | mc | 69 | 364,546 | 0/8 |
| go_api_graphql_079 | bug_invest | grep | 11 | 140,821 | 0/6 |
| go_api_graphql_079 | bug_invest | mc | 13 | 24,603 | 0/6 |
| go_api_graphql_079 | security | grep | 14 | 652,789 | 0/4 |
| go_api_graphql_079 | security | mc | 14 | 24,716 | **3/4** |
| python_api_gateway_045 | bug_invest | grep | 32 | 569,848 | **3/4** |
| python_api_gateway_045 | bug_invest | mc | 15 | 30,011 | 0/4 |
| python_api_gateway_045 | security | grep | 37 | 376,715 | 0/7 |
| python_api_gateway_045 | security | mc | 8 | 13,368 | 0/7 |
| python_api_gateway_081 | bug_invest | grep | 11 | 98,353 | 0/5 |
| python_api_gateway_081 | bug_invest | mc | 43 | 334,470 | 0/5 |

**Totals:** grep 3,345,054 in_tok, quality 6/34. mc 791,714 in_tok, quality 3/34.

### Read (n=6, one replicate — directional only)

1. **Cost: max-context is dramatically cheaper here — 0.24× grep's input tokens
   overall** (791K vs 3.35M). Per-scenario mc/grep ratios: 0.04, 0.04, 0.05, 0.17,
   0.24, and one outlier 3.40×. This INVERTS the earlier feature-task result and
   matches the in-house efficiency story: when the agent doesn't need to dump whole
   files into context, the index is far leaner. grep's cost explodes on
   security_analysis (it re-reads large files: 1.5M, 653K, 377K tokens).
2. **Quality: both arms are BAD, and the scores are nearly noise.** grep 6/34
   (18%), mc 3/34 (9%). Of 6 scenarios, 4 had at least one arm score 0, and the two
   "wins" split one each (mc won graphql-security 3/4; grep won py-bug 3/4). This is
   NOT a clean quality signal — it's two weak arms on hard tasks graded by a strict
   single-reference rubric.
3. **The grading-validity confound is now the dominant problem, not coverage.**
   On go_api_gateway_081 security, mc found 12 real vulnerabilities and scored 0/8
   because the rubric keys on 2 specific reference findings (JWT secret in `jwt.go`,
   HTTP timeouts in `main.go`) and mc named different files. Security/architecture
   tasks have open-ended valid answers that a single ground_truth under-credits —
   for BOTH arms. The 18%/9% scores are largely an artifact of this, not capability.

### Honest conclusion so far

- **The coverage gap (90% of LoCoBench needs non-code files mc can't index) is the
  solid, verified, repo-level finding** — and the actionable product signal.
- **On the fair-fight code-only subset, mc is much cheaper (≈4× fewer tokens) at
  roughly-equal (poor) quality.** The cost win is real and consistent (5 of 6);
  the quality comparison is unreliable because LoCoBench's reference-answer rubric
  under-credits divergent-but-valid answers on open-ended categories, and the
  absolute scores are too low to separate the arms.

## batch2 — 3-arm, closed-ended bug_investigation (grep vs mc vs hybrid)

Fixed both confounds from batch1: (a) closed-ended scenarios only — single named
culprit file in ground_truth, so one reference is a fair oracle; (b) added the
realistic **hybrid** arm (grep+read+list AND the 5 MC tools — what a host looks
like with max-context installed). 6 scenarios, one per language (go/py/ts/java/
rust/c), 30 turns, Sonnet 4.6 / Haiku 4.5 judge.

| scenario (lang) | grep | max-context | hybrid |
|---|---|---|---|
| c_data_analytics | 0/6 (71K) | 2/6 (40K) | 1/6 (102K) |
| go_fintech_banking | 0/6 (390K) | **truncated** (328K) | 0/6 (444K) |
| java_api_rest | 0/5 (174K) | **3/5** (153K) | 0/5 (143K) |
| python_blockchain_defi | **truncated** (1.23M) | **4/6** (152K) | 0/6 (842K) |
| rust_api_gateway | 0/6 (195K) | 0/6 (29K) | 0/6 (80K) |
| typescript_api_microservice | 2/6 (22K) | 2/6 (16K) | 2/6 (28K) |

**Paired totals (4 scenarios where all 3 arms completed):**
- grep: 462K tok, quality **2/23 (9%)**
- max-context: 238K tok, quality **7/23 (30%)**
- hybrid: 353K tok, quality **3/23 (13%)**

### What this says (n=6, 1 replicate — directional, surprising, needs replication)

1. **mc-only won quality here, clearly: 30% vs grep 9% vs hybrid 13%** on the
   fair-oracle closed-ended set — AND at ~half grep's tokens. On bug_investigation
   with a specific culprit, the call-graph/definition tools point the agent at the
   right file faster than text search. This is the FIRST quality signal favouring
   max-context on LoCoBench, and it's on the cleanest-graded category.
2. **Hybrid UNDERperformed mc-only (13% vs 30%) — the counterintuitive result.**
   Giving the agent both toolsets made it *worse* than max-context alone here.
   Likely mechanism (from transcripts): with grep+read available, the agent
   reverts to reading whole files (hybrid token use balloons — 842K on the python
   scenario vs mc's 152K), diluting the focused call-graph approach that scored.
   More tools ≠ better; the agent's tool-choice policy matters.
3. **grep truncated on python_blockchain_defi after 1.23M tokens** (re-reading
   large files), and mc truncated on go_fintech. Truncations are status-tracked,
   excluded from the paired quality totals.
4. **Absolute quality is still low (best 30%)** — these are hard tasks and a strict
   single-file oracle. The RANKING (mc > hybrid > grep) is the signal, not the
   levels.

### Honest caveats

- n=6, one replicate, one model. The python (mc 4/6) and java (mc 3/5) wins carry
  most of mc's lead; remove either and it narrows. Needs replication before any
  claim.
- The hybrid<mc result is the most important thing to verify — it contradicts the
  "additive can only help" intuition and, if real, says max-context's value is
  partly in *constraining* the agent away from expensive whole-file reads. A
  bigger closed-ended batch is the next step.

## batch2+batch3 POOLED — closed-ended bug_investigation, n=22 (cmd/pool)

22 single-culprit bug_investigation scenarios (6 batch2 + 16 batch3), balanced
across 8 languages, 3 arms each, fair mirrored playbooks, Sonnet 4.6 / Haiku 4.5,
temp 0. Paired comparisons exclude scenarios where either compared arm truncated
(quality is never scored 0 for a non-answer). This SUPERSEDES batch2-alone.

**Completion + raw quality (all 22):**
- grep: 20 completed, quality 22/106 (21%)
- max-context: 19 completed, quality 24/101 (24%)
- hybrid: 21 completed, quality 14/112 (12%)

**Paired (both arms completed):**

| pair | n | quality | tokens (ratio) | sign test |
|------|---|---------|----------------|-----------|
| grep vs mc | 17 | grep 23% / mc 21% | mc **0.43×** grep | grep 4, mc 3, **ties 10** |
| grep vs hybrid | 19 | grep 22% / hybrid 14% | hybrid 0.85× grep | grep 4, hybrid 2, ties 13 |
| mc vs hybrid | 19 | mc 24% / hybrid 13% | hybrid **2.41×** mc | **mc 7, hybrid 2, ties 10** |

### Conclusions (n=22, 1 replicate, 1 model — the strongest LoCoBench result so far)

1. **Quality: max-context ties grep on closed-ended bug-hunting.** 23% vs 21%, sign
   test 4–3 with 10 ties — a statistical wash. **batch2's apparent mc quality win
   (30% vs 9%) did NOT survive replication** — it was small-sample noise from two
   lucky scenarios. The honest result is parity, matching the in-house study's
   "equal quality" finding (NOT a quality win).
2. **Cost: max-context is decisively cheaper — 0.43× grep's input tokens** at that
   equal quality. This is the robust, replicated headline: **same answer quality,
   ~57% fewer tokens** on bug-investigation. grep's cost comes from re-reading
   whole files; mc's index queries are lean.
3. **Hybrid is the surprise that HELD across both batches and grew more robust:**
   hybrid LOSES to mc-only on quality (24% vs 13%; sign test 7–2) AND costs 2.41×
   mc's tokens. It also loses to grep (14% vs 22%). Giving the agent both toolsets
   made it *worse than either alone*. Mechanism (transcripts): with read_file
   available the agent reverts to dumping whole files, inflating context and
   diluting the focused call-graph approach — and the extra context seems to hurt
   reasoning, not just cost. **"Additive tools can be net-negative" is now the
   most replicated finding here** (two independent batches, n=19 paired).

### Honest caveats

- One model (Sonnet 4.6), one replicate, temp 0. Absolute quality is low (~21–24%)
  — these are hard tasks with a strict single-file oracle; the RANKING is the
  signal, not the levels.
- The hybrid<both result is robust within this setup but UNDERSTUDIED in mechanism:
  is it the extra tokens, the tool-choice confusion, or the playbook? A follow-up
  should ablate (e.g. hybrid with a read-discouraging playbook) before treating it
  as a general claim. It may also be model-specific (Sonnet's tool-selection).
- Still only the closed-ended bug_investigation category. Open-ended categories
  remain ungradeable here (single-reference rubric); other closed categories
  (cross_file_refactoring) untested.

### What replicated vs what didn't (the value of running n=22)
- HELD: mc cheaper at parity; hybrid worse than mc-only.
- DIED: "mc wins quality" (batch2 artifact). Reporting this honestly is the point.

## Next (re-targeted)

1. **Fix the quality signal before trusting it:** restrict to bug_investigation
   scenarios that name a SPECIFIC culprit file/line (closed-ended), where a single
   reference is a fair oracle — drop security/architecture from the quality claim.
2. **Lock the cost finding:** the ≈4× token win on code-only tasks is the cleanest
   result; re-run a larger code-only batch to tighten it (it's cheap — mc legs are
   13–34K tokens).
3. **Product follow-up:** the headline remains the coverage gap — index non-code
   files for text/FTS search.
