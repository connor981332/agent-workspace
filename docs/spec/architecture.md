# Architecture

## Overview

Agent Workspace is one Go binary, `aw`, plus a small committed shell launcher, `bin/aw`, in each
workspace. There is no required daemon. Every invocation reads shared state from a local SQLite
database, does its work, and exits. State changes happen inside transactions, so concurrent
invocations from many agents stay consistent.

```
 harness hook / human / agent
            │
            ▼
   bin/aw  (POSIX sh launcher: resolve owner checkout → read pin → exec cached, verified binary)
            │
 ┌──────────▼───────────────────────────────────────────────────────────────────────────┐
 │ aw                                                                                   │
 │                                                                                      │
 │  cli ────────────── argument parsing, output (human, --json), exit codes             │
 │  config ─────────── load + merge three layers, locks, provenance, compiled cache     │
 │  state ──────────── SQLite store, schema versions, migrations, journal               │
 │  lanes ──────────── lane records, worktrees per project, warm pool, change sets      │
 │  evidence ───────── merge proof: ancestry, patch equivalence, forge exact head       │
 │  broker ─────────── resources, seats, queue, leases, preemption, eviction, recovery  │
 │  procs ──────────── starting, tracking and stopping servers (process groups)         │
 │  providers ──────── per-lane data: built-in postgres, redis; external via protocol   │
 │  lifecycle ──────── lane hooks: prepare, install, setup, reset, teardown; journaled  │
 │  runners ────────── host, devcontainer (identity, claim step, mount and port checks) │
 │  policy ─────────── tool-call decisions: shell syntax tree, path boundary, roles,    │
 │                     owner-only, team guards, secrets, delegation; fails closed       │
 │  harness ────────── adapters per harness: payload in, decision and context out       │
 │  compose ────────── AGENTS.md block, skills, MCP config, harness hook config         │
 │  forge ──────────── gh / glab adapters, publication policy                           │
 │  sessions ───────── session records, purpose, resume mapping, handoffs, recap        │
 │  doctor/inspect ─── diagnostics, project facts for onboarding, rehearsal             │
 │  upgrade ────────── version pins, changelog, managed-file regeneration, migration    │
 └──────────────────────────────────────────────────────────────────────────────────────┘
            │ extension protocol (JSON over stdin/stdout)
            ▼
   team hooks · lifecycle steps · custom providers · token acquire/release scripts
```

Modules are not code in the binary. They are versioned bundles of files and configuration that
`aw` installs into the workspace (see [Modules](#modules)).

## Why no daemon

Every feature that seems to need a daemon can be done by reconciling lazily when `aw` runs:

- **Lease recovery.** A lease records its holder's PID and process start time. Any state-changing
  command checks holders and frees dead ones, which takes microseconds per lease.
- **Idle expiry and maximum hold.** Checked when the queue is next consulted.
- **Notifications to agents.** Delivered as added context on the agent's next tool call, by the hook
  that already runs.

A required daemon would add an install step and a failure mode that every guard would have to fail
closed on. An **optional** supervisor, `aw supervise`, runs as a user service (systemd user unit or
launchd agent) for people who want expiry, eviction and desktop notifications to happen on time
rather than at the next command. The supervisor uses the same state and code paths; it has no
authority the CLI lacks.

## On-disk layout

```
~/Code/shop-workspace/                    owner checkout of the workspace repo
├── workspace.toml                        team configuration (committed)
├── .aw/version                           pinned framework version (committed, one line)
├── .aw/lock                              sha256 per platform for that version (committed)
├── .aw/managed                           hashes of managed files, for edit detection (committed)
├── bin/aw                                launcher (committed, managed)
├── AGENTS.md  ...                        instructions with generated blocks (committed)
├── modules/…                             installed module files (committed, managed)
├── web/                                  project repo (gitignored in the workspace repo)
│   └── .worktrees/03/                    lane 03's worktree of web
├── api/
│   └── .worktrees/03/
└── .worktrees/03/                        lane 03's root: a worktree of the workspace repo
    ├── web -> ../../web/.worktrees/03    relative links to the project worktrees
    ├── api -> ../../api/.worktrees/03
    └── .aw/owner                         gitignored: absolute path of the owner checkout

<workspace git dir>/aw/                   shared by every worktree of the workspace repo
├── state.db                              SQLite, WAL mode
├── state.db.bak-<schema>                 backup before each migration
├── config.cache                          compiled merged configuration
└── logs/<lane>/…                         setup and server logs

~/.config/agent-workspace/<namespace>.toml        individual layer
~/.local/share/agent-workspace/<namespace>/       personal plans, notes, skills (default location)
${XDG_CACHE_HOME:-~/.cache}/agent-workspace/bin/<version>/aw    verified binaries
```

Lane directories are numbered (`03`) and stable. A lane's **name** (`checkout-tax`) belongs to the
work currently in it and changes when the lane is recycled. Stable paths keep dependency caches,
container mounts and published ports valid across reuse.

Project worktrees live inside each project repository (`<project>/.worktrees/<lane>`) so that a
project's devcontainer, which bind-mounts the project directory, sees every lane without extra mounts. The lane root links to them so a harness launched in the lane root sees
the whole lane. The policy engine resolves these links before any path decision.

A single-repository workspace (project `directory = "."`) has no links: the lane root is the
project's worktree.

## Configuration

`workspace.toml` is the team layer. Modules contribute fragments that merge into it. The individual
file is merged last. The merged result, with provenance for every key, is compiled to
`config.cache`, keyed by the paths, sizes, mtimes and hashes of its sources. Hooks read the cache;
only a changed source triggers a rebuild.

A sketch of the schema. Exact keys are settled during implementation and recorded in a reference
document.

```toml
[workspace]
name = "Shop"
namespace = "shop"          # names state, Git metadata, environment prefixes
schema = 1
forge = "github"

[lanes]
max = 10
pool = 3                    # warm lanes kept for reuse
retention = "14d"
hostname = "lane-{lane}.localhost"

[lanes.data.db]
provider = "postgres"
url = "postgres://postgres@localhost:5432"
template = "shop_template"
databases = ["shop_development", "shop_test"]

[lanes.data.cache]
provider = "redis"
url = "redis://localhost:6379"

[projects.web]
repo = "git@github.com:shop/web.git"
lanes = "worktree"          # or "shared": lanes run the owner checkout with lane settings
runner = "devcontainer"     # or "host"
install = "npm ci"
install_stamp = ["package-lock.json"]
guards = [{ file = "config/database.yml", contains = "AW_LANE" }]

[projects.web.servers.next]
command = "exec npm run dev -- --port {port}"
port = 3000
ready = "/"

[projects.web.lifecycle]
prepare = "scripts/lane-env"
setup = { run = "npm run db:prepare", timeout = "10m" }

[resources.web-stack]
kind = "process"
capacity = 3
servers = ["web.next", "api.server"]

[commands.e2e]
project = "web"
run = "npx playwright test"
needs = ["web-stack"]

[locks]
keys = ["guards.*", "resources.*.owner_preempts"]
```

## State store

SQLite in WAL mode, through a pure-Go driver so the binary stays static. Tables hold lanes, project
worktrees, sessions, attachments, resources, seats, leases, queue entries, provider data records,
snapshots, delegations, the lifecycle journal and the cleanup journal.

- Readers never block writers. Every state change is one transaction.
- `PRAGMA user_version` holds the state schema version. See [distribution.md](distribution.md).
- Records that cannot be read protect what they describe: an unreadable lane record means the lane
  is kept, an unreadable lease means the seat is treated as held.
- Git remains the source of truth for code. The store records what Git cannot: which session is
  where, who holds what, what setup has done.

## Policy engine

Every tool call from a hooked harness passes through one decision path:

1. The harness adapter normalizes the payload into a tool call: harness, harness session ID, working
   directory, tool kind (read, write, shell, other), paths, and the command text for shell calls.
2. The session is resolved to its attachment (a lane, a pair session in the owner checkout, or none),
   with a generation check so a stale session cannot act on a lane that has moved on.
3. Shell commands are parsed into a syntax tree with `mvdan.cc/sh`. Every simple command, redirect and
   literal path is extracted. Substitutions whose value cannot be known make any path decision that
   depends on them fail closed.
4. Rules run in order: path boundary (after resolving links), role permissions, owner-only commands,
   destructive Git, container management, process killing, direct server starts, secret access,
   publication policy, team guards, delegation.
5. The result is allow, deny with a message that names the fix, or ask (for harnesses that support
   asking). The adapter also attaches any pending notifications for the session.
6. Any error at any step is a deny.

Ref protection does not rely on the policy engine alone: `aw` installs a Git `reference-transaction`
hook that refuses ref updates from a lane outside that lane's branches (see
[owner-checkout.md](owner-checkout.md)).

## Harness adapters

An adapter knows a harness's hook payload, how to return a decision and context, how to install
hook configuration, how to launch the harness in a directory, and how to resume a session by ID.
Payload formats are not stable interfaces, so each adapter is covered by recorded payload fixtures,
and an unrecognized payload is denied.

Built-in adapters, written in Go, cover Claude Code, Codex, Grok and OpenCode. OpenCode has no hook
command; its adapter installs a small managed plugin whose `ctx.tool.hook("execute.before", …)`
handler runs `bin/aw hook opencode` and throws on a deny, which cancels the tool call (measured with
OpenCode 2.2.6; its documentation does not state this, so the recording script re-tests it). It also
throws if the guard cannot run. OpenCode's `execute` tool runs a program whose effects `aw` cannot
read, so it is refused. A **declarative adapter**, a mapping
from a harness's payload fields and tool names to the common form, compiled into the configuration
cache, lets a team or module add another hooked harness without a new release. Harnesses whose
payloads need real parsing, such as Codex's patches, keep a built-in adapter.

A harness with no hooks gets the composed instructions and `bin/aw` as its single entry point, and
the documentation says it is not confined.

### Terminal multiplexers

`aw new`, `aw resume` and `aw pair` can open a session in a terminal multiplexer instead of the
current terminal. The individual layer chooses one with `multiplexer`: `herdr` and `tmux` are built
in, `none` is the default, and any other is a command template such as
`{ command = "zellij run --name {name} -- {cmd}" }`. Session liveness never depends on the
multiplexer: it follows the harness process, whatever its parent is. Agents that can start panes
through a multiplexer's API (herdr has a socket API and CLI) are guarded like any other command, and
a lane agent cannot use one to run commands outside its lane.

The harness parity suite runs the same scenario table through every adapter and requires identical
decisions.

## Extension protocol

Team hooks, lifecycle steps, custom providers and token scripts are executables in any language.
Simple lifecycle steps are plain commands with the environment contract in
[lifecycle.md](lifecycle.md#environment-contract). Anything that returns structured data speaks the
extension protocol:

- `aw` runs the executable with a verb as its first argument (for a provider: `env`, `create`,
  `reset`, `snapshot`, `restore`, `snapshots`, `drop`, `describe`).
- A JSON request is written to standard input. It always includes `protocol` (an integer version),
  the lane, its paths and the configuration block for that extension.
- The executable writes one JSON object to standard output: `{"ok": true, ...}` or
  `{"ok": false, "error": "...", "fix": "..."}`. Standard error goes to the lane's log.
- A non-zero exit, unparseable output or a timeout is a failure. The protocol version lets old
  extensions keep working when the protocol grows.

Built-in providers implement the same interface in Go, so a team can replace one with its own.

## Runners

- **Host:** commands run directly in the lane's project worktree, with the lane's environment.
- **Devcontainer:** commands run with `docker exec` in the project's one devcontainer, in the lane's
  worktree path, which is identical inside and outside the container. The container is the one VS
  Code uses: the Compose project name is pinned to the name VS Code derives, the container is started
  only through `devcontainer up`, and a claim step handles containers left over from a moved checkout. The runner compares a running container's mounts and published ports with what the
  configuration requires and refuses a mismatch with the fix.

A project's devcontainer must be a Compose one, and its `devcontainer.json` must say:

```jsonc
{
  "dockerComposeFile": ["compose.yaml", "aw.compose.yaml"],
  "service": "app",
  "workspaceFolder": "${localWorkspaceFolder}",        // the same path inside and outside (S7)
  "initializeCommand": "../bin/aw containers claim web" // `bin/aw` for a single-repository workspace
}
```

`aw.compose.yaml` is written by `aw`, in the clone's `.devcontainer`, and kept out of Git through the
clone's `.git/info/exclude`, because it names this checkout's path. It holds the same-path bind mount,
every seat's port on loopback, and the Compose name. The team commits `devcontainer.json`, which lists
it. `aw containers claim <project>` rewrites it, and removes a container left over from a moved
checkout, or refuses when that other checkout is live and running there. The CLI runs
`initializeCommand` before it reads the Compose files, so VS Code gets exactly what `aw` gets.
A server listening only on the container's loopback is not reachable through a published port, so
servers must bind `0.0.0.0`.

The CLI itself is pinned under `[devcontainer]`: `command`, `version` and `env` (S16). `aw doctor`
checks the wiring and the pin from the files alone, and asks no container engine (R2).

**Which commands run in the container:** `aw run`, team commands, servers, and the `install`, `setup`,
`reset` and `restore` steps. `prepare`, at both levels, and `teardown` run on the host: `prepare`
writes the env files that commands in the container read, and `teardown` may run when the container
has gone. In a container, `AW_LANE_ROOT`, `AW_LANE_STATE`, `AW_OWNER` and `AW_TOOL` name host paths
that are not mounted there.

**Servers in a container (S8)** are held by a keeper, a detached host process (`aw` itself) that stays
attached through `docker exec`. The keeper's host PID is the lease holder, since a container's PIDs
may be invisible from the host. Stopping the keeper stops the server's process group in the container.
A keeper records that group, with its start time, next to the server's log; if it dies uncleanly, the
next state-changing command stops the recorded group through `docker exec`, after confirming the
start time.

**A container is in use (R49)** when a lane or the owner holds a seat on one of its servers, a setup is
running, or any exec session is live in it, which covers VS Code, terminals, `aw run` and servers.
`aw containers recreate` and `down` then refuse, and list what they found. There is no override.

Git always runs on the host, because worktree `.git` files name host paths.

## Change sets and publication

A lane has one branch in each repository, so a lane is a change set. Merge order comes from
`depends_on` on projects, producers first. `aw changeset` pushes each repository with commits and opens
merge requests linked to each other. `generated_contracts` on a project lists files agents may not
edit (R70).

With the `publication` safeguard on, `[publication] allow`, or `publication` on a project, pre-authorizes `push` (only the lane's own
branch), `open-merge-request` and `merge`. Everything else outward-facing asks. A force push, deleting
a branch, a release or a package is never pre-authorized. (Decided by the spec owner, 2026-11-09.)

## Modules

A module is a directory with a manifest, configuration fragments, files to install and instructions
to append. Kinds of content: Compose fragments for shared infrastructure, provider executables,
lifecycle hooks, guards, skills, MCP server declarations and docs.

- First-party modules ship in this repository and are versioned with the framework, so one pin
  covers both.
- Installed files are **managed**: their hashes are recorded, `aw upgrade` refreshes them, and a hand
  edit is detected and reported instead of overwritten. A team that wants to own a file can eject it,
  which stops management.
- Third-party modules, fetched by Git URL and commit, come after the first release.

First-party modules: mail catcher, object store, DNS stand-in, pgAdmin,
webhook forwarding, browser MCP, implementer role, plans.

## Testing architecture

- Unit tests for each package.
- A black-box acceptance suite that drives the `aw` binary with real Git and stubbed `docker` and
  `devcontainer` executables. It is seeded from the earlier implementations' tests and findings, and
  it is the main definition of correct behavior.
- The harness parity suite and recorded payload fixtures.
- Latency benchmarks: regressions and the guard budgets enforced in CI, all budgets on a developer laptop before each release.
- An end-to-end suite with real Docker, run on Linux.
- CI on Linux and macOS, amd64 and arm64.
