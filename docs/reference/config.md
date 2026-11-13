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
| `generated` | array of strings | — | Ignored paths (Git pathspecs) in the workspace repository itself that builds create and that do not block recycling a lane, such as *.pyc or tools/cache. Each project declares its own under [projects.<name>]. |

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
| `build_template` | string | — | postgres: builds the template when it does not exist, once, however many lanes start together. It runs in the owner checkout with AW_TEMPLATE and AW_OWNER set, and may use aw run. Without it, a machine with empty volumes has no template. |
| `databases` | array of strings | — | postgres: the databases each lane gets, named <database>_<lane>. |
| `url` | string | — | postgres or redis: where the server is, e.g. postgres://postgres@localhost:5432. Keep a password out of the team layer: put this key in the individual layer, or use PGPASSWORD or REDISCLI_AUTH. |
| `terminate` | boolean | `false` | postgres: end other connections to a database before dropping or restoring it. |
| `service` | string | — | Run the provider's client (psql, redis-cli) in this container instead of on the host: a Compose service with one running container, or a container's exact name. |
| `env` | table of string | — | Variables the lane's commands receive, e.g. DATABASE_URL = "{url:shop_development}". {lane} and {name} are the lane's number and name. |

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
| `runner` | one of `"host"`, `"devcontainer"` | `"host"` | Where commands run: host, or devcontainer (one container per project, shared with VS Code). Unwired Compose projects use a generated .devcontainer/aw/devcontainer.json; select its aw configuration in VS Code. |
| `local_compose` | string | — | Devcontainer runner only: a gitignored Compose file, relative to .devcontainer (no absolute path or .. component), that the project's committed devcontainer.json lists in dockerComposeFile and its initializeCommand creates when missing. aw writes its same-path mount, the seat ports and the Compose name there, replacing the whole file instead of writing aw.compose.yaml, so the committed configuration needs nothing of aw's and VS Code's Rebuild keeps the mount. |
| `install` | string | — | Installs dependencies, in each lane's worktree. Rerun when its stamp changes. |
| `install_stamp` | array of strings | — | Files, relative to the project, whose contents decide whether install reruns, e.g. package-lock.json. |
| `run` | string | — | The default command for aw run <project> with no command. |
| `needs` | array of strings | — | Resources the project's commands need. |
| `smoke` | string | — | A quick command that proves the project works in a lane. aw rehearse runs it. |
| `depends_on` | array of strings | — | Projects this one consumes. A change set merges those first (R69). |
| `publication` | array of strings | — | Actions pre-authorized in this project, as in [publication] allow. |
| `generated_contracts` | array of strings | — | Files generated from a contract, from the project root (** matches directories). Agents may not edit them (R70). |
| `secrets` | array of strings | — | Files that are not in Git, such as .env, linked read-only into every lane from the owner checkout, relative to the project. |
| `local_files` | array of strings | — | Gitignored files that are not secrets, such as a local properties file or a large generated schema, copied into every new lane from the owner checkout, relative to the project. Only the files named here are copied, as a clone where the file system can. Use secrets for anything an agent should not read. |
| `accepted_findings` | table of string | — | Readiness findings a person has reviewed, by file: the key is a path relative to the project (dir/** matches a directory), and the value says why it is fine. aw rehearse and aw doctor --projects skip them; rehearsal still refuses a script that names the Compose project of an accepted Compose file, since starting it would reach the real one. |
| `post_checkout` | one of `"run"`, `"skip"` | `"run"` | run: the repository's own post-checkout hook runs as a setup step of each lane, which can be retried. skip: it does not run in lanes. |
| `env` | table of string | — | Variables for this project's hooks and aw run commands, such as a switch one of its hooks reads. {lane} and {namespace} are filled in, as in lane_env, so a value such as "lane-{lane}" differs per lane. |
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
| `prepare` | command string, or `{ run, timeout, needs }` | — | After the workspace's prepare, before installs, in the project's worktree. |
| `setup` | command string, or `{ run, timeout, needs }` | — | After installs, for a newly created lane. |
| `reset` | command string, or `{ run, timeout, needs }` | — | Instead of setup, for a lane recycled from the pool. |
| `restore` | command string, or `{ run, timeout, needs }` | — | After a snapshot is restored into the lane. The place to migrate data behind the code. |

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
| `on_acquire` | string | — | process: a shell command run each time a seat is handed to a lane (or the owner), in its root and with its environment and AW_SEAT_<RESOURCE>, before its servers start; for example resetting a shared Kafka consumer group or flushing a shared cache, so the new holder does not take over the previous one's state. A failure gives the seat back. |
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
| `after` | array of strings | — | Lifecycle steps to wait for besides the defaults (the workspace's prepare and this project's steps), as <project>.<step>, e.g. api.setup. |

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

## `[options.<name>]`

Alternative setups the team supports, each a named option with choices, from which every developer picks one in [choose] of their individual file. Each choice is a table of ordinary configuration keys, laid over the team's: the merge order is defaults, the team, the chosen overlays, then the individual file. aw config validate checks every choice. AW_OPTION_<NAME> tells hooks and commands the choice.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `default` | string | — | The choice of a developer who picks none. *Team only.* |
| `description` | string | — | What the option decides, as aw options shows it. *Team only.* |
| `choices` | table of table | — | The choices, each a table of configuration keys, such as projects.web.runner or projects.web.env. *Team only.* |

## `[choose.<name>]`

Your choice for each of the team's [options], in your individual file: the option's name, and the choice. A team can lock an option with locks.keys = ["choose.<name>"].

Each value: string.

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

## `[roles.<name>]`

Team roles (R65). A lane takes one with aw new --role. No role can override path confinement, secrets, destructive Git or owner-only commands. implementer is built in, and denies publish and merge.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `allow` | array of strings | — | Capabilities this role has beyond the default: publish, merge, containers, servers. |
| `deny` | array of strings | — | Capabilities this role does not have: publish, merge, servers. |
| `instructions` | string | — | A file, from the workspace root, whose text a session in this role receives when it starts. |

## `[safeguards]`

Optional rules. None is on by default: aw regulates only its own lanes, seats and state, and leaves the rest to the harness and the forge.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `enable` | array of strings | — | Groups to turn on: git-safety (destructive Git), publication (ask before anything outward-facing), secrets, processes (kill and the like), containers. *Team only.* |

## `[publication]`

With the publication safeguard on, what agents may publish without asking (R72).

| Key | Type | Default | Meaning |
|---|---|---|---|
| `allow` | array of strings | — | Pre-authorized actions: push (only the lane's own branch), open-merge-request, merge. Force pushes, deleting branches, releases and packages are never pre-authorized. |

## `[delegation]`

What the human's aw delegate --scope maintenance covers, beyond aw's own maintenance commands.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `maintenance` | array of strings | — | Commands, as their first word, that a delegated session in the owner checkout may run. |

## `[lane_env.<name>]`

Variables lanes receive, grouped by whoever adds them, usually a module. {lane}, {namespace} and {port:N} are filled in.

Each value: table of string.

## `[webhooks.<name>]`

Forwarders of a provider's sandbox webhooks. The most recent lane to run aw webhook <provider> receives them.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `listen` | string | — | Runs the forwarder. {url} is where it should send events. |
| `server` | string | — | The server that receives them, as <project>.<server>. |
| `path` | string | `"/"` | The path on that server. |

## `[harnesses.<name>]`

Declarative adapters: another hooked harness, without a new release. Each key names a field of its hook payload.

`<name>`: lowercase letters, digits and hyphens, starting with a letter.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `session` | string | — | The field holding the session's ID. |
| `cwd` | string | — | The field holding the working directory. |
| `event` | string | — | The field holding the event's name. |
| `tool` | string | — | The field holding the tool's name. |
| `input` | string | — | The field holding the tool's input, an object. |
| `command` | string | — | The field of the input holding a shell command. |
| `path` | string | — | The field of the input holding a file path. |
| `events` | table of string | — | The harness's event names, each mapped to pre-tool-use, session-start, user-prompt-submit or session-end. |
| `shell_tools` | array of strings | — | Tools that run a shell command. |
| `read_tools` | array of strings | — | Tools that read files. |
| `write_tools` | array of strings | — | Tools that write files. |
| `other_tools` | array of strings | — | Tools with no path to check. |

## `[plans]`

The plan mechanism.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `enabled` | boolean | `true` | Set false to opt out: aw plan refuses and aw check-docs skips the plan rules. |

## `[devcontainer]`

The devcontainer CLI, for projects whose runner is devcontainer. Pin it: a CLI that drifts breaks builds.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `command` | string | `"npx -y @devcontainers/cli@{version}"` | Runs the CLI. {version} is replaced by version. aw doctor warns when the command names no version. |
| `version` | string | `"0.89.0"` | The CLI version the command uses. |
| `env` | table of string | — | Variables the CLI runs with. BUILDX_BAKE_ENTITLEMENTS_FS = "0" is set unless this overrides it. |
