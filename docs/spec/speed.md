# Speed

The developer's project should be the only thing anyone waits on. Booting a server, migrating a
database and installing dependencies take as long as they take. Everything the tool itself does
should be imperceptible.

This is a requirement, not an aspiration. It has budgets, and CI enforces them.

The guard budget was 5 ms until the M0 spike measured a prototype hot path at 5.6 to 6.0 ms p99 on
Linux, almost all of it process start-up ([docs/dev/spikes.md](../dev/spikes.md)). The spec owner
kept the design and set the budget to 8 ms on 2026-11-07.

## Budgets

Measured on a typical developer laptop with a warm file system cache, from process start to exit,
including the `bin/aw` launcher. Time spent in the project's own commands is excluded.

| Operation | Budget (p99) |
|---|---|
| Guard decision for one tool call | 8 ms |
| `aw whoami`, `aw config get` | 10 ms |
| `aw` dashboard and `aw recap` (local only) | 100 ms for 10 lanes across 4 repositories |
| `aw attach` from the warm pool | 100 ms |
| `aw new` until the harness launches | 150 ms |
| `aw serve` until the server command starts, with a free seat | 50 ms |
| Granting a seat to the next queued lane after a release | 50 ms |
| Owner preemption, until the evicted processes receive SIGTERM | 50 ms |

A budget ends when the command returns or, for `aw new`, when the harness launches. It excludes
every lifecycle hook: `reset`, `setup` and the rest run in the background afterwards
([lifecycle.md](lifecycle.md#creating-and-claiming)), and a command that needs one waits for exactly
that step.

Operations that depend on things outside the tool have no fixed budget, but must show progress and
must not hold locks while waiting: `git worktree add` on a large repository, dependency installs,
`devcontainer up`, forge queries, merge proof against a forge.

## The hot path

A guard decision runs on every tool call of every agent. It may only:

1. Run the launcher, which uses shell builtins (`read`, parameter expansion) to resolve the owner
   checkout and the pin, then `exec`s the cached binary. No subprocesses.
2. Read the compiled configuration cache, after checking its source files' metadata.
3. Read the session's attachment and pending notifications from SQLite.
4. Parse the command and decide.

It may not run Git, Docker, the devcontainer CLI, a forge CLI or any network request. It writes only
three things: activity, a session's ID on its first hook call, and the delivery of a notification.
None of these writes waits for a lock: a write that cannot be made at once is retried on the next
call. Notifications are therefore delivered at least once, and each carries an ID, so a repeat is
recognizable.

## Techniques

- **Compiled configuration.** Merging three TOML layers on each call is wasted work. The merged
  result is cached and rebuilt only when a source changes.
- **Git without subprocesses where it matters.** Reading a worktree's `.git` file, `HEAD` and refs
  directly is enough for most hot decisions. Subprocesses are reserved for operations that need Git's
  full logic.
- **Parallel by default.** Status across repositories, setup across projects and merge proof across
  lanes run concurrently.
- **Warm pool.** Creating a lane costs worktree creation, a full install and data provisioning, which
  is minutes on a large application. Claiming a recycled lane costs a checkout, an install that the
  stamp usually skips, and a template clone. See [lifecycle.md](lifecycle.md#warm-pool).
- **Background setup.** `aw new` and `aw attach` return once the lane is claimed. Setup continues in
  the background, and commands that need it wait for exactly the step they need.
- **Template databases.** The Postgres provider clones lane databases from a template instead of
  migrating from scratch.
- **Lazy lane claiming.** A session that only reads never claims a lane. See [ux.md](ux.md).
- **Local first.** The dashboard reads local state only. Forge status is opt-in (`--remote`).

## Enforcement

- A benchmark suite runs each budgeted operation against a fixture workspace. CI fails when a
  budget is exceeded at p99 on the Linux reference runner, GitHub-hosted `ubuntu-24.04` (amd64).
  The macOS reference runner, `macos-15` (arm64), is a virtual machine whose process start alone
  takes several milliseconds, so there CI fails on a regression against the main branch instead of
  on the budget itself: the main branch's binary is measured in the same run, interleaved.
- One budget has a separate ceiling on `ubuntu-24.04`: the dashboard and `aw recap` fail CI above
  200 ms p99. They run one `git status` per worktree (50 for 10 lanes across 4 repositories plus
  the workspace repository), and on the runner's four cores that takes 86 to 130 ms where a laptop
  takes 45. The 100 ms budget stands and is checked on the laptops. (Decided at the M2 exit.)
- A maintainer measures every budget twice per milestone on a Linux laptop and on an Apple Silicon
  laptop. Those measurements are the budgets' real test, since the budgets are defined on a
  developer laptop.
- Commands log their own duration and phase timings at debug level, so a slow report from a user can
  be diagnosed with `aw doctor --timings`.
- A pull request that adds work to the hot path must say why in its description.
