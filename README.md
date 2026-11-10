# agent-workspace

Agent Workspace is a development environment that facilitates parallel agent sessions. It works with any agentic coding tool, and prevents resource contention so many agents can get work done at the same time on one machine.

> **Status: pre-1.0.** Releases are signed and versioned, but interfaces, file formats and directory layout can still change between them. `aw upgrade` migrates a workspace from one release to the next. The code is under the [Apache-2.0 license](LICENSE).

## Getting started

```sh
curl -fsSL https://raw.githubusercontent.com/connor981332/agent-workspace/master/install.sh | sh
cd your-workspace   # a directory holding your project repositories, or a single repository
aw init
aw onboard
```

See [Getting started](docs/getting-started.md) for what those do and how to join a workspace that already uses `aw`, and the [supported harnesses](docs/harnesses.md), the [configuration reference](docs/reference/config.md) and the [safety model](docs/safety.md).

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

Agent Workspace is a single Go binary for Linux and macOS, with signed releases. It works with Claude Code, Codex, Grok and OpenCode, and other harnesses can be added in configuration.

## What it does

- gives each agent a lane: its own worktree of every repository, its own data, ports and hostname;
- queues access to what only one lane can use at a time, with the human going first and crashed holders recovered;
- pins one exact, verified version per workspace, updated through a merge request;
- runs in your project's dev container or on the host;
- removes only work it can prove was merged;
- stands in for shared services with optional modules, and helps an agent propose your configuration, which you review.

The [specification](docs/spec/README.md) has the details.

## Principles

- **The tool is never the bottleneck.** Only the project's own work, such as booting servers or migrating databases, takes noticeable time.
- **Uncertain work stays.** Cleanup removes only work it can prove was merged.
- **Guards fail closed.** If a guard cannot run, the action is blocked.
- **Honest about safety.** Hooks are guardrails for cooperative agents, not a security sandbox. Do not run untrusted agents or prompts on the strength of this tool. See the [safety model](docs/safety.md).
- **Any agent tool.** One set of instructions, not one per harness.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
