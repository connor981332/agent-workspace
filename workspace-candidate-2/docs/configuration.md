# Configuration reference

## `workspace.conf`

A sourced sh file of `VAR=value` lines, owned by the workspace. Anything left out takes the default
below.

### Identity and containers

| Setting | Default | Meaning |
|---|---|---|
| `WS_NAME` | required | Short lowercase name. Docs mount at `/<name>-workspace` in containers, and snapshots default to `<name>_snapshot_`. |
| `WS_PREFIX` | `WS_NAME` | Shared infra is Compose project `<prefix>-infra`. |
| `WS_NETWORK` | `<prefix>-dev-net` | External network every container joins. |
| `WS_INFRA_PROJECT` | `<prefix>-infra` | Override the infra Compose project name (keep an existing one's volumes). |
| `WS_REMOTE` | empty | `bin/bootstrap` clones `$WS_REMOTE/<directory>.git`. |
| `WS_CHECKOUT` | empty | The one host path of this workspace; `bin/bootstrap` warns when it differs. |
| `WS_BASE_BRANCH` | `main` | New lane branches start at `origin/<this>`, and merged-into checks use it. |
| `WS_DEVCONTAINER_CLI` | `npx -y @devcontainers/cli@0.89.0` | How `bin/ws` runs `devcontainer up`. Keep it pinned. |
| `WS_DEFAULT_SHELL` | `sh` | What `bin/ws <alias>` runs with no command. |
| `WS_INFRA_SERVICES` | empty | Long-running infra services. When all of them run, starts skip Compose; empty means Compose always runs. Modules append theirs. |

### Lanes

| Setting | Default | Meaning |
|---|---|---|
| `WS_LANE_SLOTS` | 4 | Lanes at once. Ports are `port + slot`; Redis needs fewer than 16. |
| `WS_LANE_RESOURCES` | empty | Adapters to apply, from `lanes.d/resources/` or `lib/ws/resources/`. |
| `WS_LANE_VAR` | empty | Also export the lane name under this variable (keeps an existing project's name, e.g. `FW_LANE`). |
| `WS_LANE_GUARDS` | empty | `<alias>:<file>:<text>` entries. A lane worktree whose file lacks the text is refused. |
| `WS_LANE_LINKS` | empty | `<alias>:<path>` entries. Gitignored files linked from the main checkout into each lane worktree, with relative links. |

### `postgres` adapter

| Setting | Default | Meaning |
|---|---|---|
| `WS_PG_DATABASES` | required | Main-checkout database names; a lane's are `<name>_<lane>`. |
| `WS_PG_SNAPSHOT` | first database | Which database snapshots copy. |
| `WS_PG_SNAPSHOT_PREFIX` | `<WS_NAME>_snapshot_` | Snapshot database names. |
| `WS_PG_CREATE` | 0 | 1: create empty lane databases at `lane new`. 0: `lanes.d/setup` prepares them. |
| `WS_PG_TERMINATE` | 0 | 1: end other connections before restore and drop. |
| `WS_PG_SERVICE`, `WS_PG_USER` | `postgres`, `postgres` | Where admin statements run (`docker exec` into the infra container). |

### `redis` adapter

| Setting | Default | Meaning |
|---|---|---|
| `WS_REDIS_URL` | `redis://redis:6379` | URL without a database; lanes get `/<slot>`. |
| `WS_REDIS_ENV` | `REDIS_URL` | Variable the project reads. |
| `WS_REDIS_SERVICE` | `redis` | Infra service flushed by `rm --drop-data`. |

## `projects`

```
<alias> <directory> [worktree|shared] [port]
```

`directory` is the repo name under `WS_REMOTE`, the Compose service, and `/<directory>` in the
container. `worktree` gives each lane its own worktree; `shared` runs lanes in the main checkout.
`port` is the dev server's container port, or `-`. In a lane, `bin/ws` sets `PORT=port+slot`.

## Lane env file

`.lanes/<lane>.env`, mode 600, read by Docker's `--env-file` and never sourced:

```
WS_LANE=<lane>
WS_SLOT=<n>
WS_LANE_BRANCH=<branch>
<WS_LANE_VAR>=<lane>          # if set
REDIS_URL=redis://redis:6379/<n>   # and whatever other adapters add
```

## Hooks

| File | When | Environment |
|---|---|---|
| `lanes.d/setup` | After `lane new`, and on `lane setup <lane>` | `WS_ROOT`, `WS_LANE`, `WS_SLOT`; the lane name as `$1` |
| `lanes.d/teardown` | Before `lane rm` removes anything | Same |
| `lanes.d/resources/<name>.sh` | Every `bin/lane` command | Adapter functions; see `lanes.d/resources/README.md` |

Only executable hooks run. Adapter hooks run in subshells, so they can't change `bin/lane`'s
variables.

## `.workspace-kit`

Written by `kit`: the values used to render templates (`KIT_V_*`), the port offset, the installed
modules, and the candidate commit the core came from. Commit it.
