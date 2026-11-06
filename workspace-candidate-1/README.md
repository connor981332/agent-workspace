# Agent Workspace

Run several coding agents at once on one machine without them colliding.

Each agent gets a **lane**: its own Git worktrees, its own lane state and whatever else your setup
scripts create for it, such as a database. Shared, expensive things like dev servers are handed out
through a queue of **slots**. Hooks keep each agent inside its lane, and cleanup only removes work it
can prove was merged.

It works with Claude Code, Codex and Grok, and needs Python 3.11+ and Git. It has no Python
dependencies.

> Hooks are guardrails for cooperative agents, not a security sandbox. See [Safety model](#safety-model).

## Quick start

From the top of a Git repository:

```sh
git clone https://github.com/connor981332/agent-workspace.git ~/agent-workspace
python3 ~/agent-workspace/workspace-candidate-1/aw init --tool-ref <full commit sha>
```

That adds `workspace.toml`, `bin/aw`, harness hooks, `.gitignore` entries and a generated block in
`AGENTS.md`. Review `workspace.toml`, commit the files, then:

```sh
bin/aw bootstrap      # Clones listed projects, activates lanes, runs the doctor
bin/aw new --harness claude
```

Or start your harness normally and tell it to read `AGENTS.md`. It claims a lane with `bin/aw attach`
before editing.

For a repository with several nested repos, pass `--project api=my-api --project web=my-web`.

## What you get

| Command | What it does |
|---|---|
| `bin/aw` | Overview: what needs you, what is running, what can be cleaned up |
| `bin/aw new --harness claude\|codex\|grok` | Claim a lane, set it up in the background and start an agent |
| `bin/aw attach` | Claim a lane for a session you started yourself |
| `bin/aw run <project> <command>` | Run a command in the lane, on the host or in its container |
| `bin/aw serve` / `bin/aw stop` | Take a server slot, queueing if needed, and return it |
| `bin/aw task <name>` | Run a task from `workspace.toml`, such as a database reset |
| `bin/aw finish --plan plans/x.md --summary "..."` | Record verified work as ready for review |
| `bin/aw sweep [--apply]` | Preview or perform cleanup of work proven merged |
| `bin/aw doctor` | Check the setup; every failure names its fix |
| `bin/aw delegate` | Let agents run maintenance for a limited time |

Run `bin/aw --help` for everything.

## Configuration

Everything specific to your project lives in `workspace.toml`:

```toml
[workspace]
name = "Shop"
namespace = "shop"

[lifecycle]
prepare = "scripts/lane-env"       # Writes each project's env file
setup = "scripts/lane-database"    # Creates per-lane data
teardown = "scripts/lane-drop"     # Removes it when the lane goes away

[projects.web]
directory = "."
install = ["npm", "ci"]
install_stamp = "package-lock.json"
generated = ["node_modules/", ".env"]

[projects.web.server]
command = "exec npm run dev"
port = 4000                        # In slot k: 4000 + 100 * k
env = { PORT = "{port}" }
```

The tool has no idea what a database is. Isolation beyond Git is your scripts' job, and they get the
lane number, paths and ports in their environment. See the [manifest reference](docs/manifest.md).

Commands run on the host by default. Set `runner = "docker"` to run them in a shared container per
project instead. See the [Docker guide](docs/docker.md) and [examples/](examples/).

## Safety model

Three rules shape the design, and they matter more than any single feature.

- **Uncertain work stays.** A deleted remote branch, a quiet session or a finished handoff does not
  prove anything merged. Cleanup needs Git ancestry, patch equivalence, or a forge reporting a merged
  change at the lane's exact head. Otherwise the lane is kept.
- **Guards fail closed.** If a guard cannot run, the tool call is blocked.
- **Isolation is cooperative.** Hooks read literal paths in tool calls and block common unsafe
  commands. An agent can defeat them with a computed path, and a harness without hooks is not
  confined at all. Do not run untrusted agents or prompts on the strength of this tool.

Details are in [docs/safety.md](docs/safety.md).

## Status

This is `workspace-candidate-1`, one of several candidates for this repository's final form. Its
design choices and their tradeoffs are in [docs/decisions.md](docs/decisions.md). macOS is covered by
CI tests only; see the platform notes in [docs/safety.md](docs/safety.md).

## Development

```sh
./ci.sh
```

Licensed under Apache-2.0.
