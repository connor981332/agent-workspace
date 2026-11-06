# workspace.toml reference

`workspace.toml` sits at the workspace root and is committed, so every lane carries its own copy.
Unknown keys are errors, so a typo cannot silently weaken a guard.

## `[workspace]`

| Key | Default | Meaning |
|---|---|---|
| `name` | `Workspace` | Shown in terminal output. |
| `namespace` | `aw` | Names lane state (`.lanes/.<namespace>`), Git metadata (`.git/<namespace>-*`), the lane marker and setting overrides (`<NAMESPACE>_SLOTS`). Use a different value per workspace if they share a machine. |
| `forge` | `auto` | `github`, `gitlab`, `none`, or `auto`, which recognizes hosts containing those names. With no adapter, merges are proved by Git alone and squash merges cannot be proved, so those lanes are kept. |

## `[lanes]`

Each value can be overridden by `<NAMESPACE>_<KEY>` in the environment.

| Key | Default | Meaning |
|---|---|---|
| `slots` | 2 | Lanes that may run servers at once. |
| `published_slots` | 9 | Highest usable slot. Set it to the number of ports your containers publish. |
| `port_stride` | 100 | A server in slot k listens on `port + port_stride * k`. |
| `max_lanes` | 10 | Lanes that may exist at once. |
| `lane_pool` | 2 | Merged lanes kept warm for reuse. |
| `lane_retention_days` | 14 | Age after which merged lanes are reclaimed. |
| `slot_idle_minutes` | 20 | Idle time before a slot expires. |
| `evict_grace_seconds` | 120 | How long `mine` waits for a busy lane. |
| `count_owner_servers` | 1 | Whether your own running servers use up a slot. |
| `workspace_base` | `main` | Base of the workspace branch in a new lane. |
| `project_base` | `origin/main` | Base for nested projects without their own `base`. |

## `[projects.<alias>]`

An alias is what you pass to `aw run`. It must not be `all`, `both` or `workspace`.

| Key | Meaning |
|---|---|
| `directory` | Defaults to the alias. `"."` means the workspace repository is the project, and it must then be the only project. Anything else is a nested Git repository. |
| `repo` | Clone URL, used by `aw bootstrap`. |
| `base` | Branch new lanes start from. |
| `runner` | `host` (default) or `docker`. |
| `install` | Dependency command run in the project during setup. |
| `install_stamp` | File whose hash decides whether `install` must rerun. Without it, `install` runs on every setup. |
| `generated` | Ignored files setup creates. They do not block lane removal. Any other ignored file does, which is deliberate. |

### `[projects.<alias>.server]`

| Key | Meaning |
|---|---|
| `command` | Shell command. Start it with `exec` so a stop signal reaches the server. |
| `port` | Base port. |
| `owner_port` | Port your own dev server uses, for `count_owner_servers`. |
| `url` | Template printed by `aw serve` and `aw whoami`. |
| `ready_path` | Path probed until the server answers. Default `/`. |
| `after` | Servers that must start first. `aw serve web` also starts these. |
| `log` | Extra log, relative to the project, that counts as activity. |

`[projects.<alias>.server.env]` maps names to templates, applied to every command in a lane that
holds a slot. Templates can use `{port}`, `{ports[alias]}`, `{lane}` and `{slot}`.

### `[projects.<alias>.docker]`

Required by `runner = "docker"`. `container` and `service` name the running container and its Compose
service, and `user` is the optional exec user.

### `[projects.<alias>.devcontainer]`

Optional. `service` is the Compose service of your own devcontainer, so `aw run` in the owner checkout
uses it. `workdir` defaults to `/<service>`.

## `[docker]`

| Key | Meaning |
|---|---|
| `compose_file` | Compose file for agent containers. It receives `<NAMESPACE>_MAIN` and `<NAMESPACE>_LANES_DIR`. |
| `project` | Its Compose project name. |
| `before_up` | Script run before the containers start, for shared infrastructure. |
| `status_projects` | Extra Compose projects listed by `aw status`. |

## `[lifecycle]`

Each value is a command as a string or argv list, relative to the lane unless absolute. Scripts run
on the host. Output goes to the lane's setup log.

| Key | When | Working directory |
|---|---|---|
| `prepare` | Before dependencies install | Lane |
| `setup` | After dependencies install | Lane |
| `teardown` | After the lane's worktrees are removed | Owner checkout |

Setup order is `prepare`, each project's `install`, then `setup`. A failed step leaves setup
incomplete, and `aw run` refuses until `aw setup <n>` succeeds. Failed teardown is journaled, and
`aw sweep --apply --retry <id>` resumes it.

All scripts and tasks receive:

| Variable | Meaning |
|---|---|
| `AW_LANE` | Lane number. |
| `AW_LANE_PATH` | Lane directory. |
| `AW_LANE_STATE` | Private state directory for this lane. |
| `AW_MAIN` | Owner checkout. |
| `AW_LANES_DIR` | Directory holding all lanes. |
| `AW_SHARED_STATE` | State shared by lanes, for your own lock files. |
| `AW_NAMESPACE` | The workspace namespace. |
| `AW_TOOL` | Path to `bin/aw`. |
| `AW_SLOT`, `AW_PORT_<ALIAS>` | Set when the lane holds a slot. |

`prepare` and `setup` also get `AW_IN_SETUP=1` and `AW_FRESH=1` when the lane was just allocated or
reused and its data should be recreated. During setup, `"$AW_TOOL" run <project> ...` is available.

### Env files

If `prepare` writes `$AW_LANE_STATE/<alias>.env`, every command in that project receives it, on either
runner. Use plain `KEY=VALUE` lines without quotes, because this is the `docker --env-file` format.

## `[tasks.<name>]`

`aw task <name> [arguments]` runs `command` in the lane, with the script environment above.
`description` appears in the generated instructions. `quiesce` lists servers to stop first and restart
after success, which is what you want around a database reset.

## `[guards]`

`server_patterns` is a list of regular expressions for commands that start servers. Those commands are
refused in favor of `aw serve`. Each `[[guards.commands]]` row has `pattern` and `message`, for your
own rules. Guards apply to agents' shell commands inside lanes.

## `[docs]`

`plan_folders` allows multi-file plans in those directories under `plans/`. `ignored_link_prefixes`
names top-level directories that `aw check-docs` does not resolve links into.

## `[tool]`

| Key | Meaning |
|---|---|
| `source`, `ref`, `subdir` | The tool pin: a Git URL, a full commit SHA and the directory holding `src/`. |
| `shims` | Extra `bin/` commands that wrap `aw`. Guards treat them as `aw`, so a wrapper cannot bypass an owner-only check. |
| `maintenance_scripts` | Other `bin/` scripts that only the owner or a maintenance delegation may run. |

`bin/aw` honors `AW_TOOL_PATH`, a source checkout that overrides the pin while working on the tool.
