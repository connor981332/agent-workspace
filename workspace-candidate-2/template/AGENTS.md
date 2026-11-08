# {{TITLE}} Workspace

> Shared instructions for coding agents (Claude Code, Codex, and others). `CLAUDE.md` imports this file. Edit only `AGENTS.md`.

<!-- Describe the product in two or three sentences: what it is and who it is for. -->

This directory is the **workspace root**. It is its own git repository, which tracks only workspace files: instructions, docs, plans, shared dev infrastructure, and `bin/`. The project repos are cloned inside it and excluded by `.gitignore`, so every history stays separate.

## Projects

`projects` lists the project repos. `bin/bootstrap`, `bin/ws`, and `bin/lane` read it.

| Alias | Repo | What it is | Container address | Host URL |
|---|---|---|---|---|
<!-- kit add-project appends a row here; fill in "What it is". -->

Each repo has its own `AGENTS.md`. **Read it before you touch that repo.** Its rules for verification, testing, style, and commits still apply when you work from here. This file adds the rules for working across repos and alongside other sessions. Read [docs/README.md](docs/README.md) for the documentation map and authority rules.

Other workspace contents:

| Path | What it is |
|---|---|
| `infra/compose.yaml`, `infra/compose.d/` | Shared dev services as the `{{PREFIX}}-infra` Compose project |
| `bin/ws` | Runs a command inside a project's dev container, or in a lane with `bin/ws <alias>@<lane>` |
| `bin/lane` | Parallel lanes: worktrees, a port, and per-lane data for each concurrent session, plus snapshots. See [parallel lanes](docs/development/setup.md#parallel-lanes). |
| `bin/devcontainer-init` | Host-side setup every project's dev container runs before starting |
| `bin/bootstrap` | Clones the project repos into place. Safe to rerun. |
| `docs/` | Canonical product, architecture, contract, and development documentation |

## Rules of engagement

1. **Run code only inside the dev containers.** Run the project's tools, tests, package managers, and dev servers with `bin/ws`, never on the host. Host and container builds differ, and running on the host corrupts installed dependencies and caches.
2. **Never create containers yourself.** Don't run `docker compose up` in a project's `.devcontainer/`, `docker run`, or `devcontainer up` with other paths or flags. `bin/ws` is the only way to start a project container from here. `bin/ws status` and `bin/ws infra ps|logs` are fine. Don't take the infrastructure down unless asked; `bin/ws infra` refuses to.
3. **Treat each repo separately.** Git commands in this directory act on the workspace repo, so use `git -C <repo>` or work inside the project directory. Branch, commit, and open change requests in each repo on its own. When a change spans repos, use the same branch name in each, link the change requests, and say which merges first: usually the side that produces a contract before the side that consumes it.
4. **Change cross-repo contracts in order.** Update the contract document in `docs/contracts/` first, then the producer, then the consumer. Keep old and new payloads compatible until both sides are merged.
5. **Check git state before you edit.** Run `git status` and check the branch in every repo you'll touch. The owner often has uncommitted work in progress. Don't stash, reset, or check out over it. If you need a clean tree or another branch, ask, or use a lane. **When another session is already working here, start your own lane** (`bin/lane new <name> <branch>`) rather than sharing its branch, server, or data. Worktrees of the workspace repo itself go under `.worktrees/` in the workspace root, never beside it.
6. **Lanes are held for the work, not the session.** Name a lane for the work it exists for and keep it until that work's owner checks are done. Only the session that created a lane removes it, and only when the owner says so. Never remove a lane another session or a checklist still uses; `bin/lane list` shows what is in use. See the [lane lifecycle](docs/development/setup.md#lane-lifecycle).
7. **Ask before outward-facing actions** such as pushing, opening or merging change requests, deploys, and deleting containers, volumes, lanes, or snapshots, unless the owner has asked for them.
8. **Never read or print secrets.** Dev secrets live in gitignored files that lanes link to; tools read them so you don't have to. Don't `cat` `.env` files or dump the environment.

## Development

The [shared development guide](docs/development/setup.md) owns setup, container identity, ports, lanes, and troubleshooting. Before starting a dev server, check whether one already exists. If a container's image or mounts need recreating, ask the owner before removing it; do not start an alternative container.

On the host, use the workspace checkout. Dev containers mount `docs/` and this guide read-only under `/{{NAME}}-workspace`.

<!-- modules: kit add-module appends module rules below this line. -->
