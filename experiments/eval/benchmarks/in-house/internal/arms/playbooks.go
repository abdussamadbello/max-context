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
- Stop as soon as you can answer confidently. Always cite exact repo-relative file paths.
`

// MCPlaybook coaches use of max-context's tools. Kept parallel to GrepPlaybook
// in length/effort; describes the tool surface without overselling.
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
- Prefer a targeted query over reading whole files. Stop as soon as you can answer
  confidently. Always cite exact repo-relative file paths.
`
