# Agent Workspace

`aw` lets you run several coding agents at once, on the same repositories, without them stepping on
each other or on you.

Each piece of work gets its own **lane**: a Git worktree of every project in the workspace, on its own
branch, with its own ports, hostname, databases and environment. Agents work inside their lane and
cannot write outside it. Your own checkout stays yours. When an agent is done it hands the lane off
for review, and once the work is proven merged, `aw` recycles the lane.

It works with Claude Code, Codex, OpenCode and Grok out of the box, and runs on Linux and macOS
(amd64 and arm64).

> `aw` is pre-1.0. Configuration and commands may still change between releases. `aw upgrade`
> migrates a workspace for you.

## How it fits together

```
my-workspace/                  ← the workspace repository: workspace.toml, bin/aw
├── api/                       ← a project repository (your "owner checkout" of it)
│   └── .worktrees/01/         ← lane 01's worktree of api, on branch aw/checkout-tax
├── web/
│   └── .worktrees/01/         ← lane 01's worktree of web, on the same branch
└── .worktrees/01/             ← lane 01's root: where the agent works
    ├── api -> ../../api/.worktrees/01
    └── web -> ../../web/.worktrees/01
```

- **Workspace.** A Git repository holding `workspace.toml`, which lists your projects (each its own
  Git repository) and how to install, run and serve them. A single repository can also be its own
  workspace.
- **Owner checkout.** Your normal clones of the projects. Agents may read here (reading a file their
  lane also has comes with a note pointing to the lane's copy), but their first write is refused and
  they are told to claim a lane. You can explicitly invite one to work alongside you
  with `aw pair`.
- **Lane.** One piece of work across every project. It is named from the purpose you give it
  (`"fix checkout tax rounding"` becomes `checkout-tax`) and also has a number. Every command that
  takes a lane accepts either.
- **Resources and seats.** Things lanes have to share, such as a dev stack that only fits in memory
  once, or a pool of mobile emulators. A lane takes a seat with `aw serve`, waits in a queue when
  none is free, and you (the human) go to the front of the line.
- **Pin.** Each workspace pins an exact version of `aw`, checked by checksum. `bin/aw` is a small
  launcher that runs that version, and a globally installed `aw` defers to it, so everyone on the team
  runs the same `aw` in a given workspace.

`aw` enforces lanes through the harness's tool hooks: every file write and shell command an agent
makes is checked before it runs. By default `aw` only guards what it owns (lanes, seats and its own
state). Stricter rules for Git, publishing, secrets, processes and containers are opt-in.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/connor981332/agent-workspace/master/install.sh | sh
```

This puts `aw` in `~/.local/bin`, after checking the release checksum (and its signature, if
`cosign` is installed). Set `AW_VERSION` to pick a release or `AW_INSTALL_DIR` to change the
directory.

You also need Git 2.34 or newer, `curl` or `wget`, and `sha256sum` or `shasum`, plus whichever
harness you use (`claude` by default). Docker and the devcontainer CLI are only needed if your
projects run in devcontainers or use shared infrastructure. `aw doctor` checks all of this.

## Get started

Several commands only work from your own terminal, not from inside an agent session. `aw` will tell
an agent to ask you when it tries one. Run the steps below yourself.

### Setting up a new workspace

1. **Create the workspace.** In a new Git repository, or in a directory that holds your project
   repositories:

   ```sh
   git init my-workspace && cd my-workspace
   aw init
   ```

   This writes `workspace.toml`, with one project for each Git repository it finds directly inside,
   or the repository itself if you run it in one. It also writes the pin, `bin/aw`, and the
   `.gitignore` entries.

2. **Add projects** that are not there yet:

   ```sh
   aw add project api git@github.com:acme/api.git
   ```

3. **Onboard.**

   ```sh
   aw onboard
   ```

   This is the main setup step. It runs `aw setup` (clones projects, installs hooks and dependencies,
   creates your personal configuration), reports what lanes will need (checkout hooks, local files,
   tools), and then starts an agent with a workspace-design skill. The agent drafts the rest of
   `workspace.toml` for you (install and run commands, servers and ports, per-lane databases, shared
   resources) and checks it with `aw rehearse`, which creates two lanes at once and confirms they do
   not collide. Its proposal lands on a lane branch for you to review and merge.

4. **Check the result.**

   ```sh
   aw doctor
   ```

   It names the fix for every problem it finds and writes nothing, so it is safe to run any time.

Later, `aw audit` starts an agent that talks the configuration through with you against the
repositories and what newer `aw` versions offer. It changes only what you agree to, on a lane
branch for review, and records the version it audited with. Add `--focus "question"` to start with
one topic.

Commit `workspace.toml`, `.aw/` and `bin/aw` so your team gets the same setup.

### Joining a workspace someone else set up

```sh
git clone git@github.com:acme/workspace.git && cd workspace
aw setup
```

`aw setup` is safe to rerun. It asks before starting any containers (pass `--containers` to skip the
question).

## Everyday use

### Starting work

```sh
aw new "fix checkout tax rounding"
```

This claims a lane (a pre-warmed one from the pool when there is one) and launches your harness in it.
Arguments after `--` go to the harness unchanged. Use `--mux` to open it in a new tmux or herdr
window, `--branch` to start on an existing branch, and `--harness` to use a different agent this time.

Every repository in a new lane starts from origin's default branch as last fetched, not from
whatever your own checkout has, so your unpushed commits stay yours. aw never fetches on its own:
`aw pull` fetches origin in every checkout and fast-forwards those on the default branch. `aw new`
tells you when your checkout differs from where the lane starts, or when origin was last fetched
over a day ago.

If you already started an agent in the owner checkout, it can claim a lane for itself with
`aw attach "<purpose>"`. It will be prompted to do so the first time it tries to write.

### What agents do inside a lane

| Command | What it does |
| --- | --- |
| `aw run <project> [cmd…]` | Run a command in the lane's worktree of a project, with the lane's environment (`AW_LANE`, `AW_PROJECT_PATH` and so on). With no command it runs the project's `run`. |
| `aw serve <resource>` | Take a seat and start the lane's servers on its own ports. Exits with status 3 and a queue position if every seat is taken. `--wait` waits instead. |
| `aw stop <resource>` | Give the seat back. |
| `aw snapshot <name>` / `aw restore <name>` | Save the lane's data, or replace it with a named snapshot. |
| `aw push [--as <name>]` | Push the lane's branch to origin and set its upstream. `--as` pushes it under the branch name your team's conventions want, and aw remembers that name for recap, finish and sweep. |
| `aw note "<text>"` | Keep a note on the lane of something left to do, such as a temporary change to undo. Recap shows open notes, a session starting in the lane is told them, and `aw finish` warns while any is open. `--done <n>` closes one. |
| `aw reset-branch [project…]` | Start the lane's branches over from their base, archiving what they had under `refs/aw/archive/` first. |
| `aw finish --summary "…"` | Hand off for review. Records the exact head of every repository and a note on what to verify. Refuses, listing every problem, if anything is uncommitted or off-branch. |

### Keeping track

| Command | What it does |
| --- | --- |
| `aw` | The dashboard. |
| `aw lanes` | The short list: number, name, state, purpose and location. |
| `aw recap` | Every lane in full: what needs you, what is running, each handoff. `--remote` adds merge request state from the forge. |
| `aw queue` | Who holds and who waits for each resource. |
| `aw whoami` | Whether you are in the owner checkout or a lane, and which session. |

All of these only read.

### Reviewing and cleaning up

- `aw try <lane>` checks out a lane's exact heads in your owner checkout so you can run them in your
  own environment. `aw try --back` puts everything back.
- `aw changeset` opens the lane's linked merge requests in merge order.
- `aw sweep` shows which lanes are proven merged, and `aw sweep --apply` recycles them. A lane counts
  as merged only when its heads are in the base branch, its commits are patch-equivalent, or the forge
  reports that exact head merged. A deleted remote branch is not enough.
- `aw park <lane>` protects a lane from cleanup until `aw unpark`.
- `aw discard <lane>` abandons a lane's work. Every branch is archived under `refs/aw/archive/` first,
  so nothing committed is lost.

### Picking up where you left off

- `aw resume` reopens an interrupted session with the harness's own resume. `aw resume --all --mux`
  reopens all of them after a reboot.
- `aw open <lane>` starts a fresh session on a lane, with its latest handoff as the first prompt.

### Working alongside an agent

- `aw pair` starts an agent in your owner checkout that acts for you: aw's own rules do not apply to
  it, so it may edit there, change the workspace's configuration and run your `aw` commands. Your
  team's guards and the safeguards you turned on still apply.
- `aw move-to-lane "<purpose>"` moves your uncommitted work into a new lane.
- `aw delegate` lets sessions in your checkout do routine maintenance (setup, sweep, park) for a
  limited time.

## Configuration

Configuration has four layers, later ones overriding earlier ones:

1. Framework defaults.
2. The team layer, `workspace.toml`, committed to the workspace repository.
3. The choices you made among the team's options (see below).
4. Your individual layer, `~/.config/agent-workspace/<namespace>.toml`. It holds personal choices
   such as `[user] harness` and `[user] multiplexer`. The team can lock keys with `[locks]`.

A minimal single-repository workspace:

```toml
[workspace]
name = "Solo"
namespace = "solo"
schema = 1

[projects.app]
directory = "."
install = "npm ci"
run = "npm test"

[projects.app.servers.dev]
command = "exec npm run dev -- --port {port}"
port = 5173
```

[`examples/shop/workspace.toml`](examples/shop/workspace.toml) shows a larger workspace with several
repositories, devcontainers, per-lane Postgres and Redis, shared resources and team commands.

To check or debug configuration:

```sh
aw config validate             # report every problem
aw config get lanes.max        # the effective value
aw config explain lanes.max    # and which layer set it
```

The full reference is [`docs/reference/config.md`](docs/reference/config.md).

### Team options

When a team supports more than one way to run something, such as real Kafka or an in-memory event
bus, it declares an option, and each developer picks a choice:

```toml
# workspace.toml
[options.event-bus]
description = "How the app reaches the event bus"
default = "memory"

[options.event-bus.choices.memory]
projects.web.env = { EVENT_BUS = "memory" }

[options.event-bus.choices.real]
projects.web.runner = "devcontainer"
```

```toml
# ~/.config/agent-workspace/<namespace>.toml
[choose]
event-bus = "real"
```

`aw options` lists each option, its choices and the one in effect for you. Hooks and commands see it
as `AW_OPTION_<NAME>`, and `aw config validate` checks every choice.

### Devcontainers

A project with `runner = "devcontainer"` runs its commands in one Compose devcontainer that lanes
and VS Code share. The project's own `devcontainer.json` needs nothing of aw's: aw generates an
ignored `.devcontainer/aw/devcontainer.json` beside it, with the checkout mounted at the same path
and every seat's port published. In VS Code, choose the configuration whose name ends in `(aw)`;
Rebuild keeps that choice. If you would rather have a single configuration, list a gitignored local
Compose file in the project's `devcontainer.json` and name it with `local_compose`; aw writes its
mount and ports there.

Starting, recreating and removing containers is yours: `aw containers up`, `recreate` and `down`,
which refuse while a lane or you are using the container. When the configuration changes, aw
replaces an idle container on its next use, keeping named volumes.

### Optional safeguards

To have `aw` also refuse or ask about things outside its own lanes, turn on groups in
`workspace.toml`:

```toml
[safeguards]
enable = ["git-safety", "publication", "secrets", "processes", "containers"]
```

- `git-safety` refuses destructive Git.
- `publication` asks before anything outward-facing, such as pushing.
- `secrets` stops agents reading or printing secret files and the environment.
- `processes` refuses `kill` and the like.
- `containers` keeps container lifecycle commands with the human.

Teams can also declare their own guards under `[guards]` and roles under `[roles]`.

### Modules

`aw modules` lists first-party add-ons, and `aw add module <name>` installs one. They include a
shared Postgres, a mail catcher with an inbox per lane, an S3-compatible object store with a bucket
per lane, a per-session browser for agents, and webhook forwarding to the lane that registered last.

## Upgrading

```sh
git switch -c upgrade-aw
aw upgrade
```

`aw upgrade` updates the pin, migrates `workspace.toml`, regenerates the files `aw` manages, and runs
`aw doctor`. Commit the result and review it as a merge request. Files you edited by hand are never
overwritten. You choose to keep them (`--eject`) or replace them (`--take`).
When the versions you skipped change keys your workspace sets, `aw upgrade` points you to
`aw audit` to go through what is new together.

## Building from source

```sh
go build -o aw ./cmd/aw
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for tests and development.

## License

See [LICENSE](LICENSE).
