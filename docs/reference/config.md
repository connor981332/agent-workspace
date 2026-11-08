# Configuration reference

<!-- Generated from internal/config/schema.go by TestReference. Do not edit by hand: change the
schema, then run AW_UPDATE_GOLDEN=1 go test ./internal/config. -->

The configuration has three layers, merged in this order (requirement R6):

1. **default**: built into aw, shown in the Default column below.
2. **team**: `workspace.toml` in the owner checkout, committed.
3. **individual**: `~/.config/agent-workspace/<namespace>.toml` (or under `$XDG_CONFIG_HOME`), personal.

Tables merge key by key; every other value, arrays and lifecycle hooks included, replaces the value of
the layer before it. Keys marked *team only* may not appear in the individual layer. The team can
lock more keys with `[locks] keys`; an individual setting of a locked key is refused and reported
by `aw config validate` and `aw config explain` (R7).

Every unknown key and invalid value is an error that names the file, the line and the fix (R9).
`aw config get <key>` prints a value, and `aw config explain <key>` shows every layer that set
it (R8). Durations are written as strings: `"30s"`, `"15m"`, `"2h"`, `"14d"`.

The configuration schema version is 1 (`[workspace] schema`).

## `[workspace]`

The workspace itself. Team layer only.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | — | Display name. *Team only.* |
| `namespace` | string | — | Names the state, Git metadata, environment prefixes and the individual file (~/.config/agent-workspace/<namespace>.toml). Lowercase letters and digits, starting with a letter. *Team only.* |
| `schema` | integer | — | Configuration schema version. This release reads schema 1. *Team only.* |
| `forge` | one of `"auto"`, `"github"`, `"gitlab"`, `"none"` | `"auto"` | Where merge requests live: auto (from each remote's host), github, gitlab or none. |

## `[lanes]`

Lanes: the numbered, stable directories agents work in.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `max` | integer, 1 to 999 | `10` | Most lanes that may exist at once. |
| `pool` | integer, at least 0 | `3` | Clean lanes kept warm for reuse. |
| `retention` | duration | `"14d"` | Pooled lanes older than this are destroyed. |
| `hostname` | string | `"lane-{lane}.localhost"` | Each lane's hostname. {lane} is the lane number, {namespace} the namespace. |
| `server_capacity` | integer, at least 1 | `2` | Capacity of the implicit resource "servers", which holds every server not listed in a resource. |
| `port_stride` | integer, at least 1 | `100` | A server in seat k listens on port + port_stride × k. |
| `branch_prefix` | string | `"aw/"` | Prefix of the branches lanes create: aw/checkout-tax. |

### `[lanes.data.<name>]`

Per-lane data, owned by a provider. The key names the data, e.g. db.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `provider` | one of `"postgres"`, `"redis"`, `"external"` | — | postgres, redis, or external (through the extension protocol). |
| `command` | string | — | For provider = "external": the provider executable. |
| `template` | string | — | postgres: the template database lanes are cloned from. Nothing ever connects to it. |
| `databases` | array of strings | — | postgres: the databases each lane gets, named <database>_<lane>. |
| `service` | string | — | Run the provider's client (psql, redis-cli) in this container service instead of on the host. |
| `env` | table of string | — | Variables the lane's commands receive, e.g. DATABASE_URL = "{url:shop_development}". |

## `[lifecycle]`

Workspace-level lifecycle hooks.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `prepare` | command string, or `{ run, timeout, needs }` | — | After data is provisioned, before installs. Writes each project's env file. |
| `teardown` | command string, or `{ run, timeout, needs }` | — | Before data is dropped, when a lane is destroyed. |

## `[projects.<name>]`

The project repositories. The key is the project's name.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `directory` | string | — | Where the repository lives, relative to the workspace. Defaults to the project's name. "." makes a single-repository workspace. |
| `repo` | string | — | Clone URL. |
| `lanes` | one of `"worktree"`, `"shared"` | `"worktree"` | worktree: every lane gets a worktree. shared: lanes run the owner checkout with lane settings. |
| `runner` | one of `"host"`, `"devcontainer"` | `"host"` | Where commands run: host, or devcontainer (one container per project, shared with VS Code). |
| `install` | string | — | Installs dependencies. Rerun when its stamp changes. |
| `run` | string | — | The default command for aw run <project> with no command. |
| `needs` | array of strings | — | Resources the project's commands need. |
| `secrets` | array of strings | — | Files that are not in Git, such as .env, linked read-only into every lane from the owner checkout, relative to the project. |
| `generated` | array of strings | — | Ignored paths (Git pathspecs) that builds create and that do not block recycling a lane, such as node_modules or tmp. Other ignored files may be someone's work, so they keep the lane. |

### `[projects."<name>".lane_guards.<name>]`

Checks a branch must pass before a lane is created on it (R28). The key names the guard.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `file` | string | — | A file in the branch, relative to the project. |
| `matches` | string | — | A regular expression the file must match. |
| `message` | string | — | What is wrong with a branch that fails, and how to fix it. |

### `[projects."<name>".lifecycle]`

Per-project lifecycle hooks.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `setup` | command string, or `{ run, timeout, needs }` | — | After installs, for a newly created lane. |
| `reset` | command string, or `{ run, timeout, needs }` | — | Instead of setup, for a lane recycled from the pool. |

### `[projects."<name>".servers.<name>]`

Servers the project runs. The key names the server, e.g. rails.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `command` | string | — | Starts the server. {port} is the seat's port. Should exec the server process. |
| `port` | integer, 1 to 65535 | — | Base port, used by the owner's seat 0. Seat k uses port + port_stride × k. |
| `owner_port` | integer, 1 to 65535 | — | A process listening here is the owner's copy, started without aw. |
| `after` | array of strings | — | Servers that must start first: <server> in the same project, or <project>.<server>. |
| `ready` | string | — | A command that exits 0 when the server is ready. |

## `[resources.<name>]`

Resources lanes queue for. The key names the resource.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `kind` | one of `"process"`, `"token"` | — | process: owns servers. token: hands out a handle. |
| `capacity` | integer, at least 1 | — | Seats. For a token resource with tokens, the number of tokens. |
| `servers` | array of strings | — | process: the servers, as <project>.<server>, started and stopped together. |
| `tokens` | array of strings | — | token: the handles, e.g. emulator-5554. |
| `acquire` | string | — | token: extension-protocol command that returns a new token. |
| `release` | string | — | token: extension-protocol command that releases a token. |
| `owner_counts` | boolean | `true` | The owner's own copy uses a seat. |
| `owner_preempts` | boolean | `true` | The owner cuts in line and evicts the longest holder. |
| `evict_grace` | duration | `"15s"` | SIGTERM to the process group, then SIGKILL after this. |
| `max_hold` | duration | `"0s"` | While others wait, a holder is evicted after this. 0 means no limit. |
| `idle_expiry` | duration | `"0s"` | A lease with no activity for this long is released. 0 means never. |
| `memory` | string | — | Approximate memory per seat, e.g. "12GB", for aw doctor's machine check. |

## `[commands.<name>]`

Named team commands, run with aw <name>.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `project` | string | — | The project the command runs in. |
| `run` | string | — | The command. |
| `needs` | array of strings | — | Resources it holds while it runs. |
| `after` | array of strings | — | Lifecycle steps to wait for besides the defaults (prepare, install, setup or reset). |

## `[guards.<name>]`

The team's own guards (R54), decided in-process on every tool call. The key names the guard. To stop people overriding them, lock guards.*.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `commands` | array of strings | — | Command patterns, such as "terraform apply". * matches any one word. Options may come between the words, and a word whose value cannot be determined counts as a match. |
| `paths` | array of strings | — | Files agents may not write, from the workspace root. ** matches any number of directories. A lane's copy of a file counts as that file. |
| `action` | one of `"deny"`, `"ask"` | `"deny"` | What happens to a matching call. |
| `message` | string | — | Why it is refused, and what to do instead. |
| `requires` | array of strings | — | Files that must exist, from the workspace root. While one is missing, everything the guard matches is refused. |

## `[locks]`

Keys the individual layer may not override. Team layer only.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `keys` | array of strings | — | Key patterns. * matches one key segment; a pattern locks everything under it. *Team only.* |

## `[user]`

Personal preferences, usually set in the individual layer.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `harness` | string | `"claude"` | The harness aw new launches: claude, codex, grok, opencode, or another on PATH. |
| `multiplexer` | `none`, `herdr`, `tmux`, or `{ command }` | `"none"` | Where aw new, resume and pair open sessions with --mux: none, herdr, tmux, or a command template, { command = "zellij run --name {name} -- {cmd}" }. |
| `auto_sweep` | boolean | `false` | Recycle proven-merged lanes in the background. |
| `owner_sessions` | one of `"normal"`, `"pair"` | `"normal"` | normal: sessions in the owner checkout must attach to a lane before writing. pair: every session started in the owner checkout is a pair session. |

## `[ports]`

Machine-specific port settings.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `offset` | integer, 0 to 60000 | `0` | Added to every port of this workspace, so two workspaces on one machine do not collide. |
