# CoReQA benchmark arm

**STATUS:** ⬜ planned — second to run (cheap, retrieval axis).

**Source:** CoReQA: Uncovering Potentials of Language Models in Code Repository
Question Answering (2025). Natural-language questions answered over an entire
repository.

## Why

Maps cleanly to our **retrieval axis** ("where / what is X?") at repo scale, and
is much cheaper than the agentic-edit benchmarks — good confirmation run after
LoCoBench. Tests whether `query_codebase` + `get_definition` + `get_architecture`
let the model answer repo-level questions with less context than grep+read.

## Open questions

- [ ] Confirm dataset availability + license (find the released artifact / repo).
- [ ] Map QA pairs to our grading: NL-answer scoring needs a judge rubric like the
      in-house edit tasks (different model, no self-judging).

## Layout (planned)

```
coreqa/
├── go.mod
├── cmd/ internal/ tasks/ results/
└── FINDINGS.md
```
