# Agent Workspace, candidate 2

A **workspace root** for running several coding agents at once on a multi-repo, container-based
project. The workspace is its own git repository. It holds the project's instructions, docs, and
shared dev infrastructure, and the project repos are cloned inside it. Every project runs in one dev
container that VS Code and the command line share. Concurrent sessions each take a **lane**: their
own worktrees, ports, and databases on the shared servers.

It is POSIX shell plus Docker, Compose, git, and the devcontainer CLI. Nothing runs that you can't
read in a few minutes.

> This is `workspace-candidate-2`, generalized from the FabWorks workspace (a Rails app and a
> TypeScript worker sharing Postgres, Redis, object storage, and mail). It is research material for
> the final agent-workspace, not a release. [docs/origin.md](docs/origin.md) maps every fabworkspace
> file to its place here. [docs/decisions.md](docs/decisions.md) explains the design and how it
> differs from candidate 1, and [docs/findings.md](docs/findings.md) records what running parallel
> agents taught the original.

## The model

```
~/Code/shop-workspace/            the workspace repo (docs, rules, infra, bin/)
├── AGENTS.md  CLAUDE.md          rules every agent reads
├── workspace.conf  projects      names, lanes, resources; one line per project repo
├── infra/compose.yaml            shared Postgres, Redis, ...  → Compose project shop-infra
├── infra/compose.d/*.yaml        services added by modules
├── lanes.d/                      lane setup/teardown hooks, workspace lane resources
├── bin/ws  bin/lane  ...         the core (kit update refreshes it)
├── .lanes/<lane>.env             per-lane settings (gitignored)
├── web/                          project repo (gitignored here), its own history
│   ├── .devcontainer/            compose.yaml pins name: web_devcontainer
│   └── .worktrees/<lane>/        lane worktrees, inside the bind mount
└── worker/                       another project repo
```

- **One container per project, found by identity, never created twice.** A project's directory name
  is its Compose service and its path in the container (`/web`). Its Compose project is pinned to the
  name VS Code derives (`web_devcontainer`). `bin/ws` starts it through `devcontainer up`, so VS Code
  and agents attach to the same container. A **claim step** in `initializeCommand` removes a container
  left over from another checkout path, which would otherwise mount an empty folder.
- **Shared infrastructure exists once.** It runs as a separate Compose project on an external network.
  Whichever project starts first brings it up, and a warm start skips Compose.
- **A lane is a slot plus copies of contended resources.** A lane in slot N gets a worktree of every
  `worktree` project, dev server ports `port + N`, a database set `<db>_<lane>`, and Redis database N.
  Lanes share containers and servers; nothing per lane is a new container.
- **Agents are governed by instructions and a single entry point.** `AGENTS.md` rules (run code only
  through `bin/ws`, never create containers, start a lane when another session is active) and
  Claude Code permission denies on secret files. No hooks are involved.

## Quick start

```sh
git clone https://github.com/connor981332/agent-workspace.git ~/agent-workspace
K=~/agent-workspace/workspace-candidate-2/kit

$K init ~/Code/shop-workspace --name shop --remote git@github.com:me --port-offset 10
$K add-project ~/Code/shop-workspace app web --lanes worktree --port 3000 --user rails
$K add-project ~/Code/shop-workspace worker shop-worker
$K add-module ~/Code/shop-workspace mail-catcher       # optional; see below

cd ~/Code/shop-workspace
$EDITOR workspace.conf AGENTS.md   # database names, lane guards and links, product description
bin/bootstrap                      # clone project repos (after the first commit and push)
bin/ws app --up
```

In each project, make it read the lane: append `_$WS_LANE` to its database names, read `REDIS_URL`
and `PORT`, and commit the generated `.devcontainer/` (replace its `Dockerfile` base with the
project's toolchain). [examples/rails-and-worker](examples/rails-and-worker/) shows a filled-in
workspace shaped like the original.

## Commands

| Command | Does |
|---|---|
| `bin/ws <alias> <cmd>` | Run in the project's container, main checkout |
| `bin/ws <alias>@<lane> <cmd>` | Run in the lane's worktree with its env file and `PORT` |
| `bin/ws <alias>` / `--up` | Shell, or just make sure the container runs |
| `bin/ws status` | Infrastructure, project containers with their checkout paths, lanes |
| `bin/ws infra <compose args>` | Compose over `infra/` and `infra/compose.d/`; refuses `down` |
| `bin/lane new <name> <branch>` | Slot, worktrees (same branch in every repo), links, env, resources, `lanes.d/setup` |
| `bin/lane list` / `show <name>` | What lanes exist, and what one holds |
| `bin/lane snapshot\|restore <name> <snap>` | Server-side copies of the lane's data |
| `bin/lane rm <name> [--drop-data] [--force]` | Refuses while worktrees hold unsaved work |
| `bin/bootstrap` | Clone the project repos; warns when the checkout path moved |

| `kit` command | Does |
|---|---|
| `kit init <dir> --name N ...` | Render `template/` and copy `core/` into a new workspace |
| `kit add-project <dir> <alias> <directory> ...` | `projects`, `.gitignore`, docs rows, and the repo's `.devcontainer/` |
| `kit add-module <dir> <module>` | Install an optional module |
| `kit update <dir>` | Replace core files; never touches owned files |
| `kit check <dir>` | Report drift and wiring problems |

## Lane resources

`WS_LANE_RESOURCES` lists adapters, which are small sh files with `env`, `create`, `drop`,
`snapshot`, `restore`, `snapshots`, and `describe` hooks. The core ships two:

- **postgres:** databases `<db>_<lane>` for each name in `WS_PG_DATABASES`, dropped by name (never
  through project code). Snapshots use `CREATE DATABASE … TEMPLATE`.
- **redis:** `REDIS_URL=<url>/<slot>`, flushed on `--drop-data`.

A workspace adds its own adapters in `lanes.d/resources/`, and those win over core adapters. See
[docs/configuration.md](docs/configuration.md).

## Modules

Optional pieces, installed with `kit add-module`. Several are deliberately half-built. Their READMEs
say what is missing, because the gaps are part of the research.

| Module | What | State |
|---|---|---|
| [mail-catcher](modules/mail-catcher/) | Mailpit for all dev email | Works; lanes share the inbox |
| [object-store](modules/object-store/) | Garage S3 with bucket, key, CORS | Half-built: lanes share the bucket |
| [dns-standin](modules/dns-standin/) | CoreDNS serving a fixture zone | Half-built: one hand-edited zone |
| [pgadmin](modules/pgadmin/) | pgAdmin for the shared Postgres | Works |
| [webhook-forward](modules/webhook-forward/) | Provider sandbox webhooks to a lane | Half-built: Stripe only, lanes see each other's events |
| [browser-mcp](modules/browser-mcp/) | Playwright MCP, a headless browser per session | Works (Claude Code) |
| [implementer-agent](modules/implementer-agent/) | Subagent that implements a brief and reports | Works (Claude Code) |
| [plans](modules/plans/) | Plan index and the snapshot-fixture verification practice | Documentation |

## Limits

- Workspaces are always multi-repo: a workspace repo with project repos inside. A single-repo project
  would be a workspace with one project.
- Isolation is cooperative. Agents follow `AGENTS.md`, and nothing stops one from running
  `docker compose up` itself. Lanes isolate data by naming, so a project that ignores `WS_LANE` uses
  the main checkout's databases (`WS_LANE_GUARDS` catches old branches, not bugs).
- Slots are fixed and few (Redis has 16 databases). There is no queue for slots and no automatic
  cleanup. Lanes are held for a piece of work and removed on purpose.
- Linux is exercised end to end (`tests/smoke-docker.sh`). macOS runs the unit suite in CI only.

## Development

```sh
./ci.sh                                  # unit suite: stubbed docker, real git; no network
tests/smoke-docker.sh <empty scratch dir> # end to end with Docker and the devcontainer CLI
```

Licensed under Apache-2.0, like the repository.
