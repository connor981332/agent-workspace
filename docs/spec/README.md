# Agent Workspace specification

This is the specification for the release candidate of Agent Workspace: the version we intend to
publish as open source. It records what the tool must do and the design we chose for it. An
implementation plan is written separately, from these documents alone, so anything a planner needs
must be written here.

The two candidates in this repository are research material. [decisions.md](decisions.md) says what
the release candidate takes from each and why. The release candidate is a new Go codebase, not a
refactor of either.

## North star

Several coding agents work on one product, on one machine, at the same time, without colliding, and
the developer keeps working alongside them as if they weren't there. The conventions that make this
safe are enforced by the tool, not requested in instruction files. The tool is generic: teams and
individuals specialize it in layers. It never loses work, and it is never what anyone waits on.

## Principles

1. **The tool is never the bottleneck.** Tool calls feel instantaneous. Only the user's project
   (booting a server, migrating a database, installing dependencies) takes noticeable time. See
   [speed.md](speed.md).
2. **Uncertain work stays.** Cleanup removes only work it can prove was merged.
3. **Guards fail closed.** If a guard cannot decide, the action is blocked.
4. **Honest about safety.** Guards stop accidents by cooperative agents. They are not a sandbox.
5. **The owner checkout belongs to the human.** Agents never disturb it unless the human invites one
   in. See [owner-checkout.md](owner-checkout.md).
6. **Any agent tool.** One set of instructions and the same guard behavior across harnesses.
7. **Agents propose, the tool verifies, a human approves.** Agents can author workspace
   configuration, but the deterministic tool checks it and a person reviews changes to the workspace
   itself.
8. **Every failure names its fix.**

## Three levels

| Level | Lives in | Holds |
|---|---|---|
| **Framework** | this repository, shipped as the `aw` binary and its modules | Generic machinery. Knows nothing about any product. |
| **Team** | the team's workspace repository | Projects, resources, lane hooks, commands, guards, instructions, shared docs and plans. |
| **Individual** | `~/.config/agent-workspace/` and gitignored files | Personal overrides, default harness, plans, notes, skills. |

Individual settings override team settings, except where the team locks them. `aw config explain`
shows where any value came from.

## Glossary

| Term | Meaning |
|---|---|
| **Workspace** | A team's workspace repository plus the project repositories inside it. A single-repository project is a workspace with one project at `"."`. |
| **Owner checkout** | The human's own checkout of the workspace and of each project. Never a lane. |
| **Project** | One repository in the workspace, with its install steps, servers and lifecycle hooks. |
| **Lane** | An isolated place for one piece of work: a worktree in each lane-enabled project, its own provisioned data and its own branch. Lanes belong to work, not to sessions. |
| **Session** | One run of an agent harness (Claude Code, Codex and others). A session attaches to a lane, or pairs in the owner checkout. Lanes outlive sessions. |
| **Harness** | An agentic coding tool. A harness adapter turns its hook payloads into the tool's common form. |
| **Resource** | Something scarce that lanes take turns using, such as a 12 GB Rails server or an Android emulator. It has a capacity and a queue. |
| **Seat** | One unit of a resource's capacity. Seat 0 is reserved for the owner. |
| **Lease** | A lane's or the owner's hold on a seat. Leases expire, can be preempted by the owner and are recovered when their holder dies. |
| **Provider** | Code that creates, resets, snapshots and drops one kind of per-lane data, such as a Postgres database set. |
| **Lifecycle hook** | A team command run at a fixed point in a lane's life, such as `bin/rails db:prepare` after creation. |
| **Module** | A versioned bundle of optional pieces (infrastructure, providers, instructions, skills, MCP configuration) that a team installs. |
| **Pair session** | A session the human has invited to work in the owner checkout. |
| **Change set** | One piece of work spanning several repositories: the same branch in each, with linked merge requests and a merge order. |
| **Handoff** | The record a session leaves when it finishes: exact heads, a note and released resources. |

## Documents

| Document | Contents |
|---|---|
| [requirements.md](requirements.md) | Numbered, testable requirements. Supersedes the [wishlist](../../wishlist.md). |
| [architecture.md](architecture.md) | Components, on-disk layout, state store, policy engine, extension protocol. |
| [speed.md](speed.md) | Latency budgets and the rules for the hot path. |
| [resources.md](resources.md) | Lanes versus resources, the queue, owner preemption, eviction and recovery. |
| [lifecycle.md](lifecycle.md) | Lane states, providers, lifecycle hooks, the warm pool, cleanup and merge proof. |
| [ux.md](ux.md) | CLI principles, commands, and the new, resume, recap and finish flows. |
| [owner-checkout.md](owner-checkout.md) | How the human's checkout is protected, and pair sessions. |
| [distribution.md](distribution.md) | Binaries, pins, onboarding, upgrades and state migration. |
| [onboarding.md](onboarding.md) | Agentic onboarding skills, the readiness audit and rehearsal. |
| [decisions.md](decisions.md) | Each design decision, its alternative and its cost, compared with the candidates. |

## Open questions

These are deliberately undecided. The implementation plan should either decide them or schedule the
decision.

1. Whether `aw upgrade` activates a new version immediately (with state migration) or offers a drain
   that waits until no lane holds a lease. Current lean: immediate, with `--drain` as an option.
2. Budget-based capacity (a machine-wide memory pool shared by resources of different cost). The
   lease model allows it; the admission rule is not designed. See [resources.md](resources.md).
3. Per-lane isolation for external services that are shared today: mail inboxes, webhook routing and
   provider sandbox accounts. See [lifecycle.md](lifecycle.md#external-service-stand-ins).
4. Real sandboxing as an optional runner. Out of scope for the first release.
5. Which harnesses are supported at first release beyond Claude Code and Codex.
