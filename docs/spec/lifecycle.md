# Lane lifecycle

## States

```
                     setting-up ──(a step fails)──▶ incomplete
                         │                              │ aw setup <lane>
          claim          ▼         finish               ▼  proven merged
 pool ──────────────▶ working ──────────────▶ ready ──────────────────────▶ recyclable
  ▲                  │      ▲                  │                               │
  │             park │      │ unpark           │ more work                     │ recycle
  │                  ▼      │                  ▼                               │
  │                 parked ─┘               working                            │
  └────────────────────────────────────────────────────────────────────────────┘
                                         discard (explicit) ──▶ archived, destroyed
```

| State | Meaning | Protected from cleanup |
|---|---|---|
| `pool` | Recycled, reset to the base, ready to claim | n/a |
| `setting-up` | Claimed; setup steps still running in the background | yes |
| `working` | Has work in progress; zero or more sessions attached | yes |
| `incomplete` | A setup step failed; `aw run` refuses until it is retried | yes |
| `ready` | Handed off for review; exact heads recorded | yes |
| `parked` | Shelved on purpose | yes |
| `recyclable` | Every repository proven merged and clean | no |

A lane belongs to work. Sessions attach to it and detach from it, crash or end, and the lane stays.

## Creating and claiming

`aw new` and `aw attach` claim a lane:

1. If the pool has a lane, take it. Otherwise allocate a new numbered lane, unless `[lanes] max` is
   reached, in which case the error names what can be cleaned up.
2. Record the lane's name and purpose, bump its generation, and create the lane branch in every
   lane-enabled project.
3. Return to the caller. Everything below runs in the background, and its progress is visible in `aw`.
4. Providers create or reset the lane's data.
5. Lifecycle steps run in order: `prepare`, each project's `install` (skipped when its stamp is
   unchanged), `setup` (or `reset` for a recycled lane). Independent projects run in parallel.
6. The lane becomes `working`. A command that needs setup waits for exactly the step it depends on.

Before creating worktrees, each project's lane guards run against the branch. A branch that fails
them (for example, a database configuration that ignores `AW_LANE`) is refused with the fix
(candidate 2, finding 9).

## Lifecycle hooks

A worktree is created with the repository's own Git hooks turned off, so a failing hook cannot stop a
lane from existing. A project's `post-checkout` hook then runs as a setup step, `<project>.post-checkout`,
after its install and on the host. Like any step it is journaled, and `aw setup <lane>` retries it.
`post_checkout = "skip"` turns it off. The workspace repository's own hook does not run in lanes.

Hooks are team (or module) commands run at fixed points. `prepare` can be set at both levels, and
the workspace's runs first. `setup`, `reset` and `restore` are per project. `teardown` is at the
workspace level. `install` is not a hook but a per-project key with a stamp, journaled like one: a
lane reinstalls when the command or the contents of the files in `install_stamp` change. A project
whose lanes are `shared` runs the owner's checkout, so its lanes do not install.

```toml
[lifecycle]                         # workspace level
prepare = "scripts/lane-env"        # write every project's env file
teardown = "scripts/lane-cleanup"

[projects.web.lifecycle]
setup = { run = "bin/rails db:prepare", timeout = "10m" }
reset = { run = "bin/rails db:seed:replant" }

[projects.api.lifecycle]
setup = { run = "npm run build:assets", needs = ["build-slot"] }
```

| Hook | When | Working directory |
|---|---|---|
| `prepare` | After data is provisioned, before installs | lane root or project worktree |
| `install` | Per project, when its stamp changed | project worktree |
| `setup` | After installs, for a newly created lane | project worktree |
| `reset` | Instead of `setup`, for a lane recycled from the pool | project worktree |
| `teardown` | Before data is dropped, when a lane is destroyed | lane root |
| `restore` | After a snapshot is restored into the lane (optional; the place to migrate) | project worktree |

- For a project whose runner is `devcontainer`, `install`, `setup`, `reset` and `restore` run in the
  project's container. `prepare` and `teardown` run on the host at both levels
  ([architecture.md](architecture.md#runners)).
- Hooks must be safe to rerun. `aw setup <lane>` reruns from the first incomplete step, in the
  foreground. An agent may run it for its own lane; every other use of `aw setup` is the human's.
- By default, `aw run <project>` and team commands wait for the workspace's `prepare` and their
  project's own steps. A team command can name others in `after = ["api.setup"]`. `aw serve` waits
  for the steps of its servers' projects. A command that a hook runs is part of setup, so it does
  not wait.
- `needs` makes a step queue for resources like any command.
- Each step's start, end, exit status and log path are journaled. A failed step marks the lane
  `incomplete` and names the retry command. A failed teardown is resumable with
  `aw sweep --apply --retry <id>`.
- Hooks run with the user's privileges. They are part of the workspace's own configuration, so
  changes to them need human review (requirement R63).

### Environment contract

Every hook, team command and server receives:

| Variable | Meaning |
|---|---|
| `AW_LANE` | Stable lane number, e.g. `03` |
| `AW_LANE_NAME` | The work's name, e.g. `checkout-tax` |
| `AW_LANE_ROOT` | Lane root directory |
| `AW_LANE_STATE` | Private state directory for the lane |
| `AW_PROJECT`, `AW_PROJECT_PATH` | The project the step runs in, and its worktree |
| `AW_OWNER` | Owner checkout of the workspace |
| `AW_NAMESPACE` | Workspace namespace |
| `AW_HOSTNAME` | The lane's hostname, e.g. `lane-03.localhost` |
| `AW_FRESH` | Hooks only: `1` when the lane's data was just created or reset |
| `AW_PHASE` | Hooks only: `prepare`, `install`, `post-checkout`, `setup`, `reset`, `restore` or `teardown` |
| `AW_<DATA>_*` | Variables from each provider, e.g. `AW_DB_URL`, `AW_CACHE_URL` |
| `AW_SEAT_<RESOURCE>`, `AW_PORT_<SERVER>`, `AW_TOKEN_<RESOURCE>` | Set while the lane holds a seat |
| `AW_TOOL` | Path to `bin/aw`, so hooks can call `"$AW_TOOL" run <project> …` |

If `prepare` writes `$AW_LANE_STATE/<project>.env` (plain `KEY=VALUE` lines), every command in that
project receives it, on either runner.

## Providers

Providers own per-lane data, where getting teardown wrong destroys something.

```toml
[lanes.data.db]
provider = "postgres"
url = "postgres://postgres@localhost:5432"   # a password belongs in the individual layer or PGPASSWORD
template = "shop_template"            # protected: nothing may connect to it
databases = ["shop_development", "shop_test"]
env = { DATABASE_URL = "{url:shop_development}" }

[lanes.data.cache]
provider = "redis"
url = "redis://localhost:6379"
```

`service` runs the provider's client in a container instead of on the host: a Compose service with one
running container, or a container's exact name. An ambiguous name is refused.

| Verb | Does |
|---|---|
| `env` | Return the variables a lane's commands receive |
| `create` | Create the lane's data, from the template where there is one |
| `reset` | Return the data to its initial state, for a recycled lane |
| `snapshot <name>` / `restore <name>` / `snapshots` | Named copies, e.g. after onboarding sandbox accounts |
| `drop` | Remove the lane's data, by exact name |
| `describe` | Report what exists, for `aw` and `aw doctor` |

Built in: **postgres** (lane databases `<name>_<lane>` cloned with `CREATE DATABASE … TEMPLATE`,
optional termination of connections before restore and drop) and **redis** (a database number per
lane, flushed on reset and drop). Others, such as an object-store bucket per lane, use the extension
protocol ([architecture.md](architecture.md#extension-protocol)).

Rules for every provider: drop by exact recorded name, never through project code; never connect to
the template; refuse to act on data not recorded as belonging to the lane.

Project code can make databases of its own, such as Rails' parallel test databases. The provider
records every database whose name begins with one of the lane's recorded names followed by `_`: at
`create`, after each `setup` and `reset`, and again before `reset` and `drop`. After that they are
recorded names like any other. A name recorded as another lane's, the template and snapshots are
never picked up this way.

`aw snapshot <name>`, `aw restore <name>` and `aw snapshots` work on the lane they run in, and agents
may use them. Snapshot names are shared across the workspace, so one onboarded snapshot can seed other
lanes, and a name in use is never overwritten. A Postgres snapshot copies the lane's configured
databases, not the copies project code made of them. Both commands stop the lane's servers first and
start them again afterwards, because Postgres cannot copy a database in use (candidate 2, finding 14);
`terminate = true` ends other connections before a restore or drop. Redis takes no snapshots.

Restoring a snapshot taken on an older schema leaves the data behind the code. Teams run migrations in
a `restore` hook (candidate 2, finding 13).

## External-service stand-ins

Modules provide shared stand-ins. Per-lane isolation beyond the first release is decided when
the stand-in modules are built (see [README.md](README.md#settled-questions)):

| Service | First release | Later |
|---|---|---|
| Mail catcher | One shared catcher; each lane sends with its own SMTP username, which tags its mail, and has a filtered inbox URL (decided 2026-11-09) | A real per-lane inbox |
| Object store | A bucket per lane through a provider | — |
| DNS stand-in | A fixture zone, with lane hostnames | Per-lane zones |
| Webhook forwarding | The most recent lane to run `aw webhook <provider>` receives its sandbox events, and the lane before is told (decided 2026-11-09) | Routing when several lanes share one sandbox account |
| Provider sandbox accounts | Shared; snapshots keep onboarded state | One account per lane |

## Finishing

`aw finish --summary "…" [--plan <path>]`, run by the agent:

1. Records the exact head of every repository in the lane.
2. Writes the handoff note (summary, plan link, what the human should verify).
3. Releases every lease the lane holds and stops its servers.
4. Moves the lane to `ready`. It stays protected while the human verifies it.

A session that ends without finishing leaves its lane `working`. After a crash it is shown as
interrupted.

## Merge proof

A lane becomes `recyclable` only when every repository in it is clean and has one of these:

1. Its head is an ancestor of the base branch.
2. Its commits are patch-equivalent to commits on the base, for histories without merge commits.
3. A forge reports exactly one merged change from the same repository at exactly this head.

A deleted remote branch, a handoff and a quiet session prove nothing. Ignored files that a project
does not list as `generated` block recycling, because they may be someone's work. Removal never
forces.

## Warm pool

`aw sweep` previews which lanes are recyclable and why the rest are kept. `aw sweep --apply`
recycles them: worktrees are reset to the base and cleaned, providers reset the data, the lane is
renamed back to its number and returns to the pool. The pool keeps `[lanes] pool` lanes. Lanes beyond
that, and lanes older than `[lanes] retention`, are destroyed: `teardown` runs, providers drop the
data and the worktrees are removed. Every step is journaled.

Individuals can set `auto_sweep = true`, which recycles proven-merged lanes in the background. It is
safe because it uses the same proof.

## Parking and discarding

- `aw park <lane>` releases the lane's leases, stops its servers and protects it indefinitely.
  `aw unpark` returns it to `working`.
- `aw discard <lane>` abandons unmerged work. It asks for confirmation (or `--yes` from the owner),
  archives each repository's branch under `refs/aw/archive/<lane-name>/<date>`, then destroys the
  lane. Agents cannot run it.
