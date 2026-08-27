package arms

// Playbooks are the per-arm tool guidance appended to the SHARED system
// skeleton. They are deliberately PARALLEL in length and effort so neither arm
// is advantaged by a richer prompt. The grep playbook is written to describe a
// genuinely skilled grep workflow (the anti-strawman requirement) and should be
// reviewed by a grep-sympathetic engineer before the headline run.

// GrepPlaybook coaches competent ripgrep usage: choosing precise patterns,
// using definition-anchored searches, narrowing by type/glob, and reading
// surgically. This is what a skilled engineer does at the terminal.
const GrepPlaybook = `
You have three tools for exploring the repository: grep, read_file, list_files.

Work like a skilled engineer at a terminal:
- Start by SEARCHING for the most specific term in the task. Prefer definition-anchored
  patterns: for a symbol Foo, try patterns like "class Foo", "def Foo", "function Foo",
  "Foo =", or "interface Foo" depending on the language.
- Use ripgrep flags via "args": -w for whole-word, -i for case-insensitive, -F for a
  literal (non-regex) string, -t py / -t ts to restrict by language, -g to include/exclude
  by glob, -l to list only matching files, -A/-B/-C for surrounding context.
- If a search is too noisy, make the pattern more specific or add a type/glob filter.
  If it returns nothing, broaden it or try a synonym.
- Use list_files to understand layout when a search alone is ambiguous.
- read_file with a line range to confirm a definition or read just the relevant region;
  read the whole file only when you truly need it.
- To trace who CALLS a function (and transitively, what reaches it): search for the
  function's name to find its direct callers, identify the enclosing function of each call
  site, then search for THOSE functions' names to find their callers, and repeat outward
  until no new callers appear. For a complete transitive set you must follow every level.
- Always cite exact repo-relative file paths.

Stopping discipline (IMPORTANT):
- Your goal is to ANSWER the task, not to map the whole repository. Once you have located
  the relevant code and understand the change required, STOP exploring and write the answer.
- A good answer names the exact files and functions to change and explains each change.
  Produce it as soon as you can do that confidently, even if some details remain unverified.
`

// MCPlaybook coaches use of max-context's tools. Kept parallel to GrepPlaybook
// in length/effort; describes the tool surface without overselling.
//
// NOTE (LoCoBench): the trailing "stopping discipline" paragraph was added after
// the v1 max-context arm truncated (63 tool calls, no answer) — it kept issuing
// per-symbol get_definition lookups instead of committing. The same paragraph is
// now MIRRORED in GrepPlaybook, so both arms get parallel stopping guidance and
// the comparison stays fair (parallel-playbook rule restored).
const MCPlaybook = `
You have max-context's tools for exploring the repository (see each tool's description
for its exact arguments). They query a pre-built index of the codebase.

Work efficiently:
- To FIND where a function, type, or file lives, search the index by keyword or name; it
  returns ranked results with file paths, line numbers, and snippets.
- To understand how code connects, trace the call graph in either direction (who calls a
  function, and what it calls), to a chosen depth. For a COMPLETE transitive caller set,
  trace to a sufficient depth so every indirect caller up the chain is included.
- To assess what a change affects, ask for the impact/blast-radius of the relevant files
  or symbols.
- To get oriented in an unfamiliar repo, request the architecture summary.
- Prefer a targeted query over reading whole files. Always cite exact repo-relative file paths.

Stopping discipline (IMPORTANT):
- Your goal is to ANSWER the task, not to map the whole repository. Once you have located
  the relevant code and understand the change required, STOP exploring and write the answer.
- Do not look up every symbol individually. If you find yourself making many single-symbol
  lookups, you have enough context — commit to the answer.
- A good answer names the exact files and functions to change and explains each change.
  Produce it as soon as you can do that confidently, even if some details remain unverified.
`

// HybridPlaybook coaches the realistic deployment arm, which has BOTH the file
// tools (grep / read_file / list_files) and max-context's index tools. It is the
// union of the two playbooks' guidance, kept to comparable length, and tells the
// agent how to choose between them — index for structure/navigation, file tools
// for everything else (including non-code files the index does not cover). This
// is deliberately NOT longer-per-capability than the single-tool playbooks: each
// tool family gets the same coaching it gets on its own, plus one routing note.
const HybridPlaybook = `
You have two complementary toolsets for exploring the repository:
  1. File tools — grep (ripgrep search), read_file (ranged/whole), list_files.
  2. max-context index tools — see each tool's description for exact arguments.

Choose the right tool for each step:
- To NAVIGATE code structure — find a definition, trace who calls what, assess a change's
  blast radius, or get an architecture overview — prefer the index tools; they answer from a
  pre-built index without reading whole files. For a COMPLETE transitive caller set, trace to
  a sufficient depth so every indirect caller is included.
- The index also searches supported configs, schemas, SQL, and docs. Use grep and read_file
  when you need unsupported files or exact source surrounding an indexed result.
- If a structural query and a text search disagree or come up empty, fall back to the other
  toolset rather than giving up. Always cite exact repo-relative file paths.

Stopping discipline (IMPORTANT):
- Your goal is to ANSWER the task, not to map the whole repository. Once you have located the
  relevant code and understand the change required, STOP exploring and write the answer.
- A good answer names the exact files and functions to change and explains each change.
  Produce it as soon as you can do that confidently, even if some details remain unverified.
`

// ContextPlaybook describes the deliberately one-shot compiler arm. The tool
// constraint is the mechanism under test: can one ranked package replace an
// iterative retrieval loop without reducing answer quality?
const ContextPlaybook = `
You have one tool for exploring the repository: compile_context.

Call it exactly once. It compiles structural, lexical, graph, architecture, and test
evidence for the current task into a ranked package under a fixed token budget.
The package reports its token usage, confidence, completeness, and omitted evidence.

After receiving the package, answer the task directly from the supplied evidence:
- Name exact repo-relative files and symbols when the evidence supports them.
- Explain the concrete changes required and relevant tests or impact.
- Do not call the tool again; missing or omitted evidence is part of what this arm measures.
- Do not invent details that are absent from the package. State material uncertainty briefly.
`
