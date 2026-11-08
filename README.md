# agent-workspace

Agent Workspace is a development environment that facilitates parallel agent sessions. It works with any agentic coding tool, and prevents resource contention so many agents can get work done at the same time on one machine.

> **Status: pre-release.** Nothing here is stable yet. Interfaces, file formats and directory layout will change without notice, and nothing is packaged or versioned for outside use. We plan to make this project open source once the design settles. The code is already under the [Apache-2.0 license](LICENSE).

## The problem

Running several coding agents against the same product at once goes wrong in predictable ways. They edit the same checkout, fight over one dev server or database, start and stop each other's containers, and leave behind work nobody can safely clean up. Teams patch this with hand-written conventions in agent instruction files, which are easy to ignore and hard to share.

Agent Workspace aims to make those conventions real: isolated workspaces per agent, coordinated access to shared resources, guardrails that are enforced rather than requested, and cleanup that never loses work.

## Three levels

The design separates what is generic from what belongs to a product or a person.

| Level | Lives in | Holds |
|---|---|---|
| **Framework** | this repository | The generic machinery. It knows nothing about any particular product. |
| **Team / organization** | a team's own workspace repo | Repos, commands, shared infrastructure, instructions, rules and shared plans and docs. |
| **Individual** | a developer's private area | Personal overrides, plans, notes and skills, kept out of the team repo. |

Personal settings can override the team's, except for what the team locks.

## Where things stand

This repository currently holds **candidate implementations**, competing approaches to the same problem. They exist to find out which design works, and the final form may take ideas from several.

| Candidate | Approach | State |
|---|---|---|
| [`workspace-candidate-1`](workspace-candidate-1/) | Python 3.11+, no dependencies. A single `workspace.toml` manifest describes the project, and the `aw` command manages agent "lanes", server slots, hooks and cleanup. Modeled on the RideTracker workspace. | On master, with tests and CI. Its [README](workspace-candidate-1/README.md) documents what it does. |
| [`workspace-candidate-2`](workspace-candidate-2/) | Shell-based workspace kit: `ws` and `lane` commands, a project template, and optional add-on modules (mail catcher, object store, DNS stand-in, browser MCP and others). Modeled on the FabWorks workspace. | On master, with tests and CI. Its [README](workspace-candidate-2/README.md) documents what it does. |

Both were derived from real workspaces that already run parallel agents day to day, which is why they reflect those teams' conventions. Neither is yet a general-purpose framework.

**Release candidate.** We have settled on the design for the version we will publish: a new Go codebase that takes the safety core of candidate 1 and the container and resource work of candidate 2. The candidates are now research material and the reference for its acceptance tests. The design is in the [specification](docs/spec/README.md).

## Where it is going

The target is described in the [specification](docs/spec/README.md), whose [requirements](docs/spec/requirements.md) grew out of the original [wishlist](wishlist.md): a framework that supports the three levels above and covers, among other things:

- layered configuration with visible provenance, and clone-and-go onboarding;
- pinned, deliberately updated framework versions that detect local edits;
- multi-repo isolated workspaces, per-agent provisioned data, and cross-repo change sets;
- a fair queue for single-instance resources, with human priority and crash recovery;
- dev-container execution that never starts or stops containers unexpectedly;
- named team commands, composed instructions, team-defined hooks, and human review of changes to the workspace itself;
- secrets handling, forge integration, roles, and plans and docs owned at the right level.

The specification adds two principles the wishlist did not state: the tool is never what anyone waits on, and the owner's own checkout is never disturbed by agents.

## Principles

- **The tool is never the bottleneck.** Only the project's own work, such as booting servers or migrating databases, takes noticeable time.
- **Uncertain work stays.** Cleanup removes only work it can prove was merged.
- **Guards fail closed.** If a guard cannot run, the action is blocked.
- **Honest about safety.** Hooks are guardrails for cooperative agents, not a security sandbox. Do not run untrusted agents or prompts on the strength of this tool.
- **Any agent tool.** One set of instructions, not one per harness.

## Open source plans

We intend to release this as open source. Before that happens we want to settle the design, converge on one implementation, strip out anything specific to the teams it was modeled on, and write documentation for people outside them. Until then, feel free to read and try things, but expect breakage and don't build on it yet.

## Contributing

Not open for outside contributions yet. This section will be filled in when the project is released.
