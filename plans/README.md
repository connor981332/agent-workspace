# Plans

Plans for building Agent Workspace itself. Each plan is one file, `plans/<slug>.md`, whose first
`## Status` block has these fields: **State**, **Updated** (YYYY-MM-DD), **Branches / MRs**,
**Done**, **In progress** and **Next step**, so a reader with no session memory can resume from it.
State is one of `DRAFT`, `PLANNED`, `PARTIALLY IMPLEMENTED`, `FULLY IMPLEMENTED` or
`FULLY VERIFIED`, and the row below must agree with it. This follows the plan convention from
requirement R82 until the framework
ships its own plan mechanism.

| Plan | State | Updated | Summary |
|---|---|---|---|
| [release-candidate.md](release-candidate.md) | PARTIALLY IMPLEMENTED | 2026-11-10 | Milestones, acceptance suite, module layout, benchmarks, release pipeline and CI for the Go release candidate. |
| [agent-feedback.md](agent-feedback.md) | FULLY IMPLEMENTED | 2026-11-13 | Response to the 0.1.5 agent ergonomics feedback, change requests and owner feature requests, in five phases. |
