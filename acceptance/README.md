# Acceptance suite

The black-box definition of what `aw` does (R92; [plan §3](../plans/release-candidate.md#3-acceptance-suite)).
It drives the `aw` binary, its launcher and real Git, as a user or an agent would. `docker`,
`devcontainer`, forge CLIs, database clients and harnesses are replaced by recording stubs. The
suite needs no Docker and no network (R91).

This directory is its own Go module. It never imports the main module, and
`TestNoImportsFromTheMainModule` fails if it does. The scenario files themselves do not depend on
Go: another runner could interpret them from this document.

## Running it

```sh
cd acceptance
go test ./...                              # build aw from the repository and run everything
AW_BIN=/path/to/aw go test ./...           # test a given binary (for example a release asset)
go test -run 'TestScenarios/cli/' .        # one area
go test -run 'TestScenarios/cli/version$' .
AW_ACCEPT_MILESTONE=M1 go test ./...       # also run the scenarios of a later milestone
```

The runner needs `git` and a POSIX `sh` on `PATH`.

## Scenarios

`scenarios/<area>/<name>.txtar` holds one [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript)
scenario: a short script, then the files it needs. The header is comment lines at the top:

```
# One sentence on the behavior.
# req: R25 R26          requirements it covers (plan §2)
# source: C1 test_generic.py::HostLaneTests::test_single_repo_lane_from_init_to_cleanup
# milestone: M2         from which it must pass (required)
# changed: why the expected result differs from the candidate's, when it does
```

`requirements.toml` holds `current`, the latest milestone whose scenarios must pass. A scenario of a
later milestone is reported as skipped with `pending: must pass from Mn`, so scenarios can be written
before the code. A pending scenario that has not been written yet holds the single command
`unwritten`, which fails, so it cannot pass by accident once its milestone is reached.

`in_progress` names the milestone being built. Its scenarios that have been written run and must
pass, so CI checks them on every platform from the PR that adds them; those that still hold only
`unwritten` stay pending. When the milestone's last scenario passes, that PR moves `current`
forward and `in_progress` to the next milestone.

### What every scenario gets

- An isolated `HOME`, XDG directories and Git configuration (`GIT_CONFIG_GLOBAL`, no system config,
  the identity `Acceptance Owner <owner@example.invalid>`, default branch `main`).
- `aw` on `PATH`: the suite's global `aw`. Inside a workspace that has a `bin/aw` launcher, it runs
  that launcher, as the real global `aw` will (distribution.md); elsewhere it runs the binary under
  test (`$AW_BIN`). Use `exec aw …`.
- `$STUB_LOG` and `$STUB_STATE` for the stubs, and `$AW_REPO`, the repository under test (for
  scenarios that check its examples).
- `$AW_VERSION`, the version of the binary under test (the runner builds it as `0.0.0-acceptance`),
  and `$AW_MIRROR`, a release mirror holding it for this platform, laid out as GoReleaser publishes
  it (`aw_<version>_<os>_<arch>` and `checksums.txt`). `aw init` pins `$AW_VERSION` and the
  launcher fetches from the mirror, so every scenario in a workspace goes through the launcher's
  fetch and verify path with no network.
- **The owner check.** When the scenario has built a workspace, the runner appends a final check
  that the owner checkout is exactly as the last snapshot recorded it. A scenario that changes the
  owner checkout on purpose declares `owner-may-change`.

## Conditions

Besides testscript's own (`[exec:name]`, `[unix]`, …), `[root]` is true when the suite runs as
root, which can write where the scenario expects a permission error.

## Custom commands

| Command | Does |
|---|---|
| `workspace <name> [--single] [--init] [--repos a,b] [--forge github\|gitlab\|none]` | Builds an owner checkout and changes into it. With `--init` it then runs `aw init` as a maintainer would (the archive's `workspace.toml`, if any, replacing the generated one), commits and pushes the result, and runs `aw version` once so the binary is cached. The default is a workspace repository with nested project repositories `web` and `api` (gitignored in the workspace repository). `--single` makes the workspace repository the only one. Each repository has a bare remote under `$WORK/remotes`, one commit on `main`, and tracks `origin/main`. The archive's `workspace.toml`, when present, is committed. With `--forge github` or `--forge gitlab` (the default is `none`), each `origin` is set to a forge-style URL (`https://github.com/acceptance/<repo>.git`, or `gitlab.example.com`), which the scenario's global Git configuration rewrites to the local bare repository with `url.<…>.insteadOf`, so pushes and fetches still work. The path contains spaces and a quote (`…/it's a workspace/<name>`) and is reached through a symlink (on macOS, through `/var`), so it is never canonical. Sets `$OWNER` and `$AW_TEST_FORGE`, and takes the owner snapshot. |
| `stubs <name>…` | Puts the named stubs from `stubs/` first on `PATH`. |
| `json <file> <path> [value]` | `<file>` is `stdout`, `stderr` or a file. `<path>` is `.` or a sequence of `.key` and `[n]`. With a value, the value at the path must equal it: a JSON string compares as its text, anything else as compact JSON (`0`, `true`, `null`, `[]`). Without a value, the path must exist. `!` negates. |
| `owner-snapshot` | Records the owner checkout now: for each repository, its files outside `.git` and `.worktrees` (content, mode, symlink target; ignored files included), the index (`git ls-files --stage`), `HEAD`, every ref, `refs/stash` and `.git/config` (without the `branch.<b>.*` sections of branches the owner did not have, which belong to lanes). |
| `owner-unchanged` | The owner checkout is as the last snapshot recorded it. Lane branches (`refs/heads/aw/*`) and aw's own refs (`refs/aw/*`) are not the owner's and are not compared; other new refs are allowed, except a new stash. `!` asserts that something changed. |
| `owner-may-change` | Skips the automatic owner check at the end. |
| `git-fail <args-regexp>` | Fault injection: a `git` wrapper first on `PATH` fails, with `git-fail: injected failure for: git …` on stderr, when its arguments match the extended regexp, and runs the real Git otherwise. Patterns accumulate. |
| `unwritten` | Fails. The body of a pending scenario not yet written. |
| `forge-reply <gh\|glab> <path-regexp> <file>` | Makes the `gh` or `glab` stub answer an API path matching the extended regexp with the file's contents. Without a matching rule the stub fails like an API that cannot be reached. |
| `lane-commit <NN> [<project>/]<file> <message>` | As the lane's agent: appends the message to the file in lane NN's worktree (the lane root, or the project's), then adds and commits it. |
| `git-merge <repo> <branch>`, `git-squash …`, `git-cherry-pick …` | As the human, in the owner checkout's repository (`.` or a project directory): merge the branch into the current branch (a merge commit, one squashed commit, or the commits cherry-picked), then push. The owner snapshot is taken again afterwards. |

The custom commands still to come (`session`, `hook`, `decision`, …)
are listed in plan §3.3 and are added with the milestones that need them.

## Test tools

`tools/` holds small programs the runner builds and puts on every scenario's `PATH`:

| Tool | Does |
|---|---|
| `awtest-sqlite <file> <sql>…` | Runs SQL against a SQLite file, for state aw would not write itself, such as a database from a newer release. It knows nothing of aw's tables. |

## Stubs

`stubs/` holds POSIX `sh` test doubles, ported from
[candidate 2](../workspace-candidate-2/tests/stubs/). Each appends its argv to `$STUB_LOG` and
answers from files under `$STUB_STATE`; the comment at the top of each stub lists those files.

## The harness's own tests

`testdata/harness/*.txtar` test the commands above (`TestHarness`). A file named `*-fails.txtar`
must fail, with the text of its `# want-failure:` line in the failure. They show that a change to
the owner checkout's files, ignored files, index, refs, stash or Git configuration fails a scenario,
and that lanes, new branches and state do not.

## Coverage

`coverage/` checks that `requirements.toml` lists exactly the requirements in
[requirements.md](../docs/spec/requirements.md), with the first milestone the plan's §2 gives each,
that every `# req:` tag names a real requirement, and that every requirement whose milestone has
been reached is cited by a scenario that runs. A requirement no scenario can show is marked
`covered_by = "bench"` (a latency budget, checked by the benchmarks) or `covered_by = "ci"` (the
platform matrix and the suite itself).
