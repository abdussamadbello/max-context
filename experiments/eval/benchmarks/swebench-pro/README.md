# SWE-bench Pro benchmark arm

**STATUS:** ⬜ planned — run only if LoCoBench / CoReQA look promising (expensive).

**Source:** SWE-Bench Pro: Can AI Agents Solve Long-Horizon Software Engineering
Tasks? — arXiv [2509.16941](https://arxiv.org/abs/2509.16941) (Sep 2025, rev. Nov 2025).
1,865 problems across 41 repositories; multi-file, "hours-to-days" tasks.

## Why

The 2025 successor to SWE-bench Verified for the **edit + cost** headline. Same
paired-arm design (max-context vs grep, same model/budget): does cheaper retrieval
*also* lift solve rate on long-horizon tasks? Our in-house data shows the call-graph
cost advantage **grows with chain depth** — long-horizon multi-file tasks are
exactly where that should compound. Being new (Sep 2025) lowers contamination risk
vs the older Verified set.

## Caveats (decide before spending)

- **Expensive:** real agent loops, real model spend, 1,865 problems → run a
  stratified subset, not the full set.
- **Ambiguous loss:** measures downstream task success, not retrieval in isolation.
  A tie is still a win for us (cheaper at parity); a loss is hard to attribute.
- Python-heavy historically — confirm language spread before claiming multilingual.

## Layout (planned)

```
swebench-pro/
├── go.mod
├── cmd/ internal/ tasks/ results/
└── FINDINGS.md
```
