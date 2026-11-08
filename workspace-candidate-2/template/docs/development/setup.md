# Shared development setup

**Status: Development reference.** Run project code only in the dev containers. The [workspace rules](../../AGENTS.md) define authorization and repository coordination.

## Prerequisites and checkout

Clone to `{{CHECKOUT}}`. Dev containers are identified by their host path, so use this path every time.

```sh
git clone <workspace repo> {{CHECKOUT}}
cd {{CHECKOUT}}
bin/bootstrap
bin/ws <alias> --up
```

Requires Docker, Node (for the pinned devcontainer CLI), and access to the repos. Then open `{{CHECKOUT}}/<repo>` in VS Code and choose "Reopen in Container". VS Code attaches to the container `bin/ws` started.

## Dev containers

### One container per project, shared by VS Code and the CLI

| Project | Compose project | Workspace in container |
|---|---|---|
| each project `<dir>` | `<dir>_devcontainer` | `/<dir>` |
| (shared) | `{{PREFIX}}-infra` | — |

Why VS Code and `bin/ws` always land in the same container:

- **Shared engine.** The VS Code Dev Containers extension and `@devcontainers/cli` find an existing container by its labels: `devcontainer.local_folder` (the project's host path) and `devcontainer.config_file`. If one exists, they attach to it or start it instead of creating a new one.
- **Pinned Compose project name.** Each project's `.devcontainer/compose.yaml` sets `name: <dir>_devcontainer`, the same name VS Code derives. Every tool, including plain `docker compose`, uses the same project.
- **Claim step.** `initializeCommand` runs `bin/devcontainer-init` on the host before either tool starts a container. If the project's container was created from a *different* checkout path, the script removes it, because that container would bind-mount a missing folder and show an empty workspace. If that other checkout is live and its container is running, the script refuses to start and says what to stop.
- **Shared infrastructure in its own Compose project.** `bin/devcontainer-init` also creates the `{{PREFIX}}-dev-net` network and starts `infra/` if a service is missing. Whichever project starts first brings the infrastructure up, so start order doesn't matter. Rebuilding a project container doesn't touch the database or queue.

For this to hold, **open each project from the same host path every time**: `{{CHECKOUT}}/<repo>`. The path is part of the container's identity. Don't open a project through a symlink or a copy.

### Running commands: `bin/ws`

```sh
bin/ws <alias> <command>     # in the project's container and main checkout
bin/ws <alias>               # interactive shell
bin/ws <alias> --up          # only make sure the container is running
bin/ws <alias>@<lane> <cmd>  # in a lane: its worktree, port, and data (see Parallel lanes)
bin/ws status                # infrastructure, project containers and their checkout paths, lanes
bin/ws infra ps              # docker compose on infra/ and infra/compose.d/
```

If the container isn't running, `bin/ws` starts it with `devcontainer up`, going through the same `initializeCommand` VS Code uses. It then runs the command with `docker exec` as the image's user in the workspace folder, which is fast when the container is warm. The first start builds the image and installs dependencies. If a project's dev image changes, the old container must be removed so it can be rebuilt; ask the owner first.

Long-running processes such as dev servers belong in a VS Code terminal or a background process, not a blocking foreground command. Before you start one, check whether it is already running. Don't start a second one.

### Ports

Host ports are published by Compose, so they work whether VS Code or `bin/ws` started the container. Pick them so they don't collide with other workspaces on the same machine.

| Service | Host | In `{{PREFIX}}-dev-net` |
|---|---|---|
| Postgres | `localhost:{{PORT:5432}}` (postgres / password) | `postgres:5432` |
| Redis | `localhost:{{PORT:6379}}` | `redis:6379` |
<!-- ports: kit add-project and kit add-module append rows here. -->

### Workspace worktrees

A worktree of the workspace repo itself (for docs or instruction changes while the main checkout is busy) lives in `.worktrees/<name>` under the workspace root, which is gitignored. Don't create worktrees beside the workspace; everything stays inside this directory, as `bin/lane` does for project worktrees in `<repo>/.worktrees/`.

```sh
git worktree add -b docs/x .worktrees/docs-x origin/{{BASE_BRANCH}}
```

## Parallel lanes

A lane lets another agent session work alongside the first, for example verifying one milestone while the next is built. Each lane has its own worktree and branch of every `worktree` project, its own dev server ports, and its own copy of every lane resource, so jobs, results, and data never cross. Lanes share the containers and the infrastructure servers.

```sh
bin/lane new m4 m4/stage-one      # worktrees, slot 1, the lane's data, then lanes.d/setup
bin/lane list
bin/lane show m4
bin/ws <alias>@m4 <command>        # in the lane
bin/lane snapshot m4 onboarded     # save the lane's data (stop its servers first)
bin/lane restore m4 onboarded      # put that copy back
bin/lane rm m4 [--drop-data]       # refuses while the worktrees hold unsaved work
```

- **What `bin/ws <alias>@<lane>` changes.** It runs in `<repo>/.worktrees/<lane>` for `worktree` projects (the main checkout for `shared` ones), passes `.lanes/<lane>.env` (gitignored) into the container, and sets `PORT` to the project's port plus the lane's slot.
- **What a project must do.** Read the lane variable (`WS_LANE`) and the resource variables the lane sets, for example appending `_<lane>` to database names. `WS_LANE_GUARDS` refuses lanes on branches that predate this.
- **Git runs on the host.** A worktree's `.git` file names its host path, which the container can't see, so run git for a lane from the host.
- **Secrets are shared.** Each worktree links the main checkout's gitignored secret files (`WS_LANE_LINKS`), so sessions, signed IDs, and encrypted columns read the same in every lane.
- **Snapshots** copy data server-side. Stop the lane's servers and consumers before taking or restoring one. Snapshots of onboarded test accounts are how slow, human-gated setup is done once and reused.
- **Lane ports** are published by each container from the start (`port+1` … `port+WS_LANE_SLOTS`). A container created before a slot existed doesn't publish it until it is recreated (ask the owner); tests still work without a published port.

### Lane lifecycle

Slots are few, so a lane is held for as long as the work it exists for, and freed when that work is done.

- **A lane belongs to a piece of work.** Name it for the milestone or stage, and keep it until that work's owner checks are done, not until the session ends. A session that finishes early leaves its lane in place and says so in its report.
- **Only the session that created a lane removes it, and only when the owner says so.**
- **Before `bin/lane rm <name> --drop-data`, check that nothing is lost.** `bin/lane rm` refuses while a worktree has uncommitted changes or a branch that is neither pushed nor merged. Save anything worth keeping in the data with `bin/lane snapshot` first. Never remove a snapshot other work depends on.
- **Workspace-repo worktrees** follow the same rule: remove one with `git worktree remove` once its branch has merged.

## Adding a project repo

Run `kit add-project <workspace> <alias> <directory> [--lanes worktree|shared] [--port N] [--host-port N] [--user U]` from the agent-workspace checkout. It adds the line to `projects` and `.gitignore`, a row to this guide's port table, and writes `.devcontainer/` into the repo if it has none. Then pick host ports that don't collide, and put any new shared service in `infra/`, never in the project.

## If something looks wrong

```sh
bin/ws status
```

- **More than one container per project, or a `devcontainer-*` container,** means something bypassed the pinned name. Tell the owner rather than deleting it yourself.
- **A rebuild fails with "additional privileges requested"** (Docker Compose 5 builds through buildx bake): `bin/ws` sets `BUILDX_BAKE_ENTITLEMENTS_FS=0` for its container start. For VS Code's "Rebuild Container", start it with `bin/ws <alias> --up` first, or launch VS Code with that variable set.
- **An empty workspace inside the container** means the container is mounting an old path. Restarting through VS Code or `bin/ws` runs the claim step, which fixes it.
- **A service is unreachable:** check `bin/ws infra ps`. Running `bin/devcontainer-init` from a project directory starts anything that's missing and removes containers of services that no longer exist.

## Workspace documentation in containers

Each project's Compose configuration mounts these workspace paths read-only:

| Host source (relative to project root) | Container path |
|---|---|
| `../docs/` | `/{{NAME}}-workspace/docs/` |
| `../AGENTS.md` | `/{{NAME}}-workspace/AGENTS.md` |

Existing containers don't acquire bind mounts when Compose files change, and a restart alone doesn't add them. Ask the owner before recreating a container.
