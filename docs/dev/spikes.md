# Spikes

Results of the M0 spikes in [plans/release-candidate.md](../../plans/release-candidate.md). The
spike code lives on the branch `spike/hot-path` (`spike/`), which is never merged; its CI runs are
[42449948189](https://github.com/connor981332/agent-workspace/actions/runs/42449948189) (benchmarks)
and [42450056148](https://github.com/connor981332/agent-workspace/actions/runs/42450056148) (ref
hook, and a second benchmark run with the same results). Recorded 2026-11-07.

## T3: hot-path latency

### What was measured

A prototype `aw hook claude pre-tool-use`, run as a new process for every call, through a
prototype `bin/aw` launcher written to the shape in distribution.md (only `read` and parameter
expansion, then `exec` of the cached binary). Each call:

1. reads and decodes a Claude Code `PreToolUse` payload from standard input;
2. finds the common Git directory from the payload's `cwd` by reading `.git` and `commondir`, as
   `gitx` will;
3. reads the compiled config cache (a JSON header of source metadata checked with `stat`, then a
   26 KB gob body: 4 projects, 6 resources, 40 guards, 400 provenance entries);
4. opens the SQLite state database in WAL mode and reads the session's attachment and its pending
   notifications (two queries);
5. parses a 200-character shell command with `mvdan.cc/sh/v3/syntax` and walks it;
6. prints the decision.

Variants isolate the costs: an empty Go binary (the floor of any Go process), the prototype run
directly and through the launcher, the prototype with cobra linked (as `cmd/aw` links it), the
ncruces SQLite driver instead of modernc, a stripped binary (`-s -w`), and risk 1's fallback (a
per-session snapshot file instead of SQLite, so no SQLite is linked at all). The bench runs every
variant round-robin, 1000 times each after 50 warm-up rounds, measuring from just before process
start to exit. All times are milliseconds.

#### ubuntu-24.04 (linux/amd64), n = 1000

| Variant | p50 | p95 | p99 | max | binary |
|---|---|---|---|---|---|
| floor (Go process, direct) | 1.68 | 1.84 | 1.95 | 2.26 | 2.0 MB |
| floor via launcher | 2.37 | 2.57 | 2.67 | 3.53 | 2.0 MB |
| prototype, direct | 4.22 | 4.60 | 4.76 | 8.42 | 12.1 MB |
| prototype via launcher (modernc) | 4.88 | 5.35 | 5.58 | 7.83 | 12.1 MB |
| prototype + cobra linked, via launcher | 4.89 | 5.29 | 5.43 | 6.04 | 12.3 MB |
| prototype via launcher (ncruces) | 5.01 | 5.50 | 6.61 | 12.06 | 15.5 MB |
| prototype stripped (-s -w), via launcher | 4.97 | 5.48 | 5.64 | 7.83 | 7.9 MB |
| fallback: session snapshot file, no SQLite, stripped, via launcher | 3.66 | 3.97 | 4.16 | 5.94 | 3.4 MB |

In-process phases of the modernc prototype (mean µs): payload 70, find Git dir 48, config cache 384, SQLite 727, parse 40, output 28; total 1302. Package init: 0.83 ms in all.

#### macos-15 (darwin/arm64), n = 1000

| Variant | p50 | p95 | p99 | max | binary |
|---|---|---|---|---|---|
| floor (Go process, direct) | 3.48 | 5.49 | 8.26 | 11.09 | 2.1 MB |
| floor via launcher | 7.34 | 11.85 | 15.81 | 32.27 | 2.1 MB |
| prototype, direct | 5.72 | 9.29 | 12.66 | 30.12 | 11.7 MB |
| prototype via launcher (modernc) | 9.76 | 14.78 | 22.32 | 41.52 | 11.7 MB |
| prototype + cobra linked, via launcher | 9.83 | 15.19 | 20.56 | 73.49 | 11.8 MB |
| prototype via launcher (ncruces) | 9.85 | 15.45 | 19.88 | 31.46 | 15.0 MB |
| prototype stripped (-s -w), via launcher | 9.79 | 15.12 | 21.66 | 31.05 | 7.6 MB |
| fallback: session snapshot file, no SQLite, stripped, via launcher | 8.20 | 12.44 | 18.55 | 24.72 | 3.3 MB |

In-process phases of the modernc prototype (mean µs): payload 58, find Git dir 79, config cache 216, SQLite 1977, parse 42, output 31; total 2408. Package init: 0.46 ms in all.

#### ubuntu-24.04-arm (linux/arm64), n = 1000

| Variant | p50 | p95 | p99 | max | binary |
|---|---|---|---|---|---|
| floor (Go process, direct) | 1.21 | 1.31 | 1.39 | 1.89 | 2.0 MB |
| floor via launcher | 1.69 | 1.81 | 1.87 | 2.16 | 2.0 MB |
| prototype, direct | 2.94 | 3.17 | 3.31 | 3.57 | 11.6 MB |
| prototype via launcher (modernc) | 3.45 | 3.70 | 3.87 | 4.23 | 11.6 MB |
| prototype + cobra linked, via launcher | 3.43 | 3.69 | 3.83 | 4.11 | 11.8 MB |
| prototype via launcher (ncruces) | 3.60 | 3.87 | 4.93 | 191.09 | 15.2 MB |
| prototype stripped (-s -w), via launcher | 3.46 | 3.70 | 3.88 | 4.33 | 7.6 MB |
| fallback: session snapshot file, no SQLite, stripped, via launcher | 2.52 | 2.70 | 2.85 | 3.07 | 3.2 MB |

In-process phases of the modernc prototype (mean µs): payload 51, find Git dir 44, config cache 258, SQLite 568, parse 29, output 26; total 981. Package init: 0.48 ms in all.

#### macos-15-intel (darwin/amd64), n = 1000

| Variant | p50 | p95 | p99 | max | binary |
|---|---|---|---|---|---|
| floor (Go process, direct) | 6.62 | 9.37 | 14.72 | 78.18 | 2.2 MB |
| floor via launcher | 12.12 | 17.51 | 31.42 | 90.09 | 2.2 MB |
| prototype, direct | 11.24 | 17.35 | 52.23 | 97.24 | 12.1 MB |
| prototype via launcher (modernc) | 17.12 | 26.59 | 64.89 | 106.92 | 12.1 MB |
| prototype + cobra linked, via launcher | 17.43 | 26.00 | 60.98 | 107.61 | 12.3 MB |
| prototype via launcher (ncruces) | 17.54 | 28.85 | 89.06 | 174.04 | 15.3 MB |
| prototype stripped (-s -w), via launcher | 17.26 | 27.04 | 69.91 | 149.85 | 8.0 MB |
| fallback: session snapshot file, no SQLite, stripped, via launcher | 14.35 | 20.80 | 29.83 | 87.05 | 3.5 MB |

In-process phases of the modernc prototype (mean µs): payload 121, find Git dir 104, config cache 409, SQLite 1836, parse 66, output 48; total 2589. Package init: 0.94 ms in all.

#### Linux laptop (Intel Core Ultra X7 358H, Arch, /bin/sh = bash) (linux/amd64), n = 500

| Variant | p50 | p95 | p99 | max | binary |
|---|---|---|---|---|---|
| floor (Go process, direct) | 1.26 | 1.69 | 2.01 | 2.31 | 2.1 MB |
| floor via launcher | 2.46 | 3.33 | 3.69 | 3.89 | 2.1 MB |
| prototype, direct | 2.70 | 3.44 | 3.95 | 5.27 | 12.5 MB |
| prototype via launcher (modernc) | 4.04 | 5.30 | 5.97 | 7.21 | 12.5 MB |
| prototype + cobra linked, via launcher | 3.87 | 5.17 | 5.88 | 7.50 | 12.6 MB |
| prototype via launcher (ncruces) | 4.00 | 5.25 | 5.83 | 7.16 | 15.7 MB |
| prototype stripped (-s -w), via launcher | 4.00 | 5.10 | 5.84 | 6.36 | 7.9 MB |
| fallback: session snapshot file, no SQLite, stripped, via launcher | 3.10 | 4.23 | 4.76 | 6.14 | 3.4 MB |

In-process phases of the modernc prototype (mean µs): payload 49, find Git dir 30, config cache 214, SQLite 402, parse 23, output 18; total 739. Package init: 0.52 ms in all.

### Findings

- **The 3 ms target is missed on both reference runners**, and the 5 ms budget is missed on
  ubuntu-24.04 (5.58 ms p99) and the Linux laptop (5.97 ms p99). Only ubuntu-24.04-arm meets 5 ms.
- **Process start, not the work, dominates.** The prototype's own work is 0.7 to 1.3 ms on Linux.
  An empty Go process started through the launcher already costs 2.7 ms p99 on ubuntu-24.04 and
  3.7 ms on the laptop.
- **The launcher's shell is a large share.** On ubuntu-24.04 (`/bin/sh` is dash) the launcher adds
  about 0.7 ms. On the laptop, where `/bin/sh` is bash, it adds 1.2 ms; starting bash alone costs
  0.9 ms there, against 0.28 ms for `/bin/true`. On macOS (`/bin/sh` is bash 3.2) it doubles the
  floor.
- **Linking more code costs start-up time even when it does not run.** The prototype binary
  (12.5 MB) starts about 0.5 ms slower than the empty one (2.1 MB) before doing any work.
  Package initialization is only 0.3 to 0.9 ms in all, so the cost is mostly loading the bigger
  binary. Stripping (`-s -w`, 7.9 MB) did not change the timings measurably.
- **SQLite is the largest in-process step**: 0.4 to 0.7 ms on Linux and about 2 ms on the macOS
  runners. The snapshot-file fallback saves 0.9 to 1.2 ms p50 on Linux, because it skips both the
  open and the loading of the SQLite code, but it still misses 3 ms (4.16 ms p99 on ubuntu-24.04,
  2.85 ms on ubuntu-24.04-arm).
- **Linking cobra costs nothing measurable** (within noise on every runner), so `cmd/aw`'s
  exemption from §4.3 rule 3 does not cost start-up time.
- **GitHub's macOS runners cannot hold an absolute budget.** They are virtual machines: an empty
  Go process costs 3.5 ms p50 and 8.3 ms p99 on macos-15 (14.7 ms p99 on macos-15-intel), and the
  tails are long (max 30 to 100 ms). S5 names macos-15 as a reference runner for enforcing
  budgets. These numbers say it can only show relative regressions; an absolute macOS number needs
  real Apple hardware.

### SQLite driver

Keep `modernc.org/sqlite`. ncruces/go-sqlite3 v0.37 no longer compiles WebAssembly at start-up,
and so starts as fast at p50, but its binary is 3 MB larger and its tails are worse on most
runners (ubuntu-24.04 p99 6.61 against 5.58 ms; macos-15-intel 89 against 65 ms). Neither driver
changes the conclusion: opening SQLite at all is the cost.

### Go or no-go

**No-go against the 3 ms target**, on both reference runners and on the Linux laptop. Following
the M0 exit criterion, the fallback options in risk 1 go to the spec owner before M1 starts. The
measurements above suggest these options, in order of what they save on Linux:

1. Skip the shell on the hot path: point the harness hook configuration at the cached binary, and
   have the binary check the workspace's pin itself (re-`exec`ing the right version if they
   differ). This saves about 0.7 ms with dash and 1.2 ms with bash. It changes distribution.md and
   speed.md, which say a guard decision runs the launcher.
2. Replace SQLite on the hot path with a per-session snapshot file that state changes rewrite
   (risk 1's second fallback): saves about 0.9 to 1.2 ms, and keeps SQLite code from loading. A spec
   change (architecture.md, speed.md).
3. Keep the hot-path binary small: the hook would be a separate small binary or the main binary
   would avoid linking the heavy packages, which in Go means a separate binary. About 0.5 ms.
4. Change what is enforced: the 5 ms budget is per-call on a developer laptop (speed.md), while
   the M0 target of 3 ms on shared CI runners may be stricter than the spec needs; and macOS
   budgets cannot be enforced on hosted runners at all (see above).

The laptop numbers for Apple Silicon are not yet measured (a pending manual check).

## Ref-hook behavior

### What was measured

`spike/refhook/run.sh` installs a `reference-transaction` hook through `core.hooksPath` that
records each call's state, refs, whether the old and new values are zero, and where the hook ran
(its working directory and `GIT_DIR`). It runs 29 commands from a lane worktree and from the owner
checkout, plus worktree management from the owner. It ran on Git 2.34.1 (ubuntu:22.04), 2.39.5
(debian:bookworm), Apple Git 2.39.5 (Git-154, macos-15) and 2.56.0 (alpine:edge, and the laptop).

### Findings

- **Every version fires the hook for every ref update that can be refused**, from a lane and from
  the owner checkout, including `commit --no-verify`. 2.34 is a workable minimum, as proposed in
  §9.
- **The hook can tell where it runs.** From a lane, `GIT_DIR` is the lane's
  `<common>/worktrees/<NN>` directory and the working directory is the lane. From the owner
  checkout, `GIT_DIR` is unset and the working directory is the owner checkout. This holds on every
  version.
- **States differ.** 2.34 and 2.39 call `prepared`, then `committed` or `aborted`. 2.56 adds
  `preparing` before `prepared`. The hook must decide at `prepared` (present everywhere) and exit
  at once on every other state.
- **Symbolic `HEAD` changes are invisible before 2.4x**: `git checkout <branch>`, `git switch`
  back and `git symbolic-ref HEAD` fire no hook on 2.34 and 2.39, and do on 2.56. `HEAD` is private
  to a worktree, so the rules need not depend on it.
- **Pseudo-refs appear constantly**: `ORIG_HEAD`, `CHERRY_PICK_HEAD`, `REBASE_HEAD`, and on 2.56
  also `AUTO_MERGE` (on almost every command), `BISECT_HEAD`, `BISECT_EXPECTED_REV` and
  `refs/bisect/*`. These are private to the worktree and must be allowed.
- **An old value of zero does not mean "create".** Git passes zero when the caller did not check
  the old value (`git branch -f`, `git branch -D`), so a rule cannot tell creation from update or
  deletion from the old value.
- **`git pack-refs` (and so `git gc`) from a lane touches every ref**, other branches and tags
  included: one transaction writes each ref into `packed-refs` (zero → its current value), then
  another deletes each loose copy (its value → zero). A rule that refuses other branches from a
  lane would make `git gc` and `git pack-refs` fail in lanes. Allowing any update whose resulting
  resolved value is unchanged covers both transactions, while a real deletion (`git branch -D`)
  still changes the resolved value and can be refused.
- **Shared refs from a lane:** `refs/stash` (push, pop, drop), `refs/notes/commits`, tags and
  `refs/remotes/*` (updated by `git push`, not by an up-to-date `git fetch`) are all visible and can
  be refused. `refs/stash` is shared by every worktree, which is what S3 already says.

### Consequences for S3 (proposed rule refinements)

The accepted S3 answer stands. The spike adds these details, for M3:

- decide at `prepared`; exit 0 immediately for `preparing`, `committed` and `aborted`;
- allow worktree-private pseudo-refs (`HEAD`, `ORIG_HEAD`, `FETCH_HEAD`, `CHERRY_PICK_HEAD`,
  `REBASE_HEAD`, `MERGE_HEAD`, `AUTO_MERGE`, `BISECT_*`) and `refs/bisect/*`;
- allow any update that leaves the ref's resolved value unchanged (packing);
- refuse `refs/notes/*` from a lane, alongside tags, other branches, `refs/aw/*` and `refs/stash`.

### Matrix

The refs each command presents in `prepared` (and `preparing` on 2.56), from the lane and from the
owner checkout. "(delete)" means the new value is zero. "—" means no hook call.

| From | Command | 2.34.1 | 2.39.5 | 2.39.5 (Apple Git-154) | 2.56.0 |
|---|---|---|---|---|---|
| lane | commit | `HEAD`, `refs/heads/lane-03` | `HEAD`, `refs/heads/lane-03` | `HEAD`, `refs/heads/lane-03` | `AUTO_MERGE (delete)`, `HEAD`, `refs/heads/lane-03` |
| lane | commit --no-verify | `HEAD`, `refs/heads/lane-03` | `HEAD`, `refs/heads/lane-03` | `HEAD`, `refs/heads/lane-03` | `AUTO_MERGE (delete)`, `HEAD`, `refs/heads/lane-03` |
| lane | commit --amend | `HEAD`, `refs/heads/lane-03` | `HEAD`, `refs/heads/lane-03` | `HEAD`, `refs/heads/lane-03` | `AUTO_MERGE (delete)`, `HEAD`, `refs/heads/lane-03` |
| lane | branch create | `refs/heads/lane-branch` | `refs/heads/lane-branch` | `refs/heads/lane-branch` | `refs/heads/lane-branch` |
| lane | branch -f (other) | `refs/heads/lane-branch` | `refs/heads/lane-branch` | `refs/heads/lane-branch` | `refs/heads/lane-branch` |
| lane | branch -D (other) | `refs/heads/lane-branch (delete)` | `refs/heads/lane-branch (delete)` | `refs/heads/lane-branch (delete)` | `refs/heads/lane-branch (delete)` |
| lane | tag | `refs/tags/t-lane` | `refs/tags/t-lane` | `refs/tags/t-lane` | `refs/tags/t-lane` |
| lane | tag -d | `refs/tags/t-lane (delete)` | `refs/tags/t-lane (delete)` | `refs/tags/t-lane (delete)` | `refs/tags/t-lane (delete)` |
| lane | checkout -b | `refs/heads/lane-topic` | `refs/heads/lane-topic` | `refs/heads/lane-topic` | `AUTO_MERGE (delete)`, `HEAD`, `refs/heads/lane-topic` |
| lane | checkout back | — | — | — | `AUTO_MERGE (delete)`, `HEAD` |
| lane | switch --detach | `HEAD` | `HEAD` | `HEAD` | `AUTO_MERGE (delete)`, `HEAD` |
| lane | switch back | — | — | — | `AUTO_MERGE (delete)`, `HEAD` |
| lane | reset --hard HEAD~1 | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03` | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03` | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03` | `AUTO_MERGE (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03` |
| lane | stash push | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03`, `refs/stash` | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03`, `refs/stash` | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03`, `refs/stash` | `AUTO_MERGE (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03`, `refs/stash` |
| lane | stash pop | `refs/stash (delete)` | `refs/stash (delete)` | `refs/stash (delete)` | `AUTO_MERGE`, `refs/stash (delete)` |
| lane | stash drop (after push) | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03`, `refs/stash`, `refs/stash (delete)` | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03`, `refs/stash`, `refs/stash (delete)` | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03`, `refs/stash`, `refs/stash (delete)` | `AUTO_MERGE (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03`, `refs/stash`, `refs/stash (delete)` |
| lane | cherry-pick | `CHERRY_PICK_HEAD`, `CHERRY_PICK_HEAD (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03` | `CHERRY_PICK_HEAD`, `CHERRY_PICK_HEAD (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03` | `CHERRY_PICK_HEAD`, `CHERRY_PICK_HEAD (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03` | `AUTO_MERGE`, `AUTO_MERGE (delete)`, `CHERRY_PICK_HEAD`, `CHERRY_PICK_HEAD (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/lane-03` |
| lane | merge --ff-only main | `ORIG_HEAD` | `ORIG_HEAD` | `ORIG_HEAD` | `AUTO_MERGE (delete)`, `ORIG_HEAD` |
| lane | rebase main | `REBASE_HEAD (delete)` | `REBASE_HEAD (delete)` | `REBASE_HEAD (delete)` | `AUTO_MERGE (delete)`, `REBASE_HEAD (delete)` |
| lane | fetch | — | — | — | — |
| lane | push (own branch) | `refs/remotes/origin/lane-03` | `refs/remotes/origin/lane-03` | `refs/remotes/origin/lane-03` | `refs/remotes/origin/lane-03` |
| lane | update-ref other branch | `refs/heads/main` | `refs/heads/main` | `refs/heads/main` | `refs/heads/main` |
| lane | update-ref refs/aw | `refs/aw/archive/x` | `refs/aw/archive/x` | `refs/aw/archive/x` | `refs/aw/archive/x` |
| lane | symbolic-ref HEAD | — | — | — | `HEAD` |
| lane | symbolic-ref back | `HEAD` | `HEAD` | `HEAD` | `AUTO_MERGE (delete)`, `HEAD` |
| lane | bisect start/reset | `HEAD` | `HEAD` | `HEAD` | `AUTO_MERGE (delete)`, `BISECT_EXPECTED_REV (delete)`, `BISECT_HEAD (delete)`, `HEAD`, `refs/bisect/bad`, `refs/bisect/bad (delete)`, `refs/bisect/good-<oid>`, `refs/bisect/good-<oid> (delete)` |
| lane | notes add | `refs/notes/commits` | `refs/notes/commits` | `refs/notes/commits` | `refs/notes/commits` |
| lane | pack-refs --all | `refs/aw/archive/x`, `refs/aw/archive/x (delete)`, `refs/heads/lane-03`, `refs/heads/lane-03 (delete)`, `refs/heads/lane-topic`, `refs/heads/lane-topic (delete)`, `refs/heads/main`, `refs/heads/main (delete)`, `refs/notes/commits`, `refs/notes/commits (delete)`, `refs/remotes/origin/lane-03`, `refs/remotes/origin/lane-03 (delete)`, `refs/remotes/origin/main`, `refs/remotes/origin/main (delete)` | `refs/aw/archive/x`, `refs/aw/archive/x (delete)`, `refs/heads/lane-03`, `refs/heads/lane-03 (delete)`, `refs/heads/lane-topic`, `refs/heads/lane-topic (delete)`, `refs/heads/main`, `refs/heads/main (delete)`, `refs/notes/commits`, `refs/notes/commits (delete)`, `refs/remotes/origin/lane-03`, `refs/remotes/origin/lane-03 (delete)`, `refs/remotes/origin/main`, `refs/remotes/origin/main (delete)` | `refs/aw/archive/x`, `refs/aw/archive/x (delete)`, `refs/heads/lane-03`, `refs/heads/lane-03 (delete)`, `refs/heads/lane-topic`, `refs/heads/lane-topic (delete)`, `refs/heads/main`, `refs/heads/main (delete)`, `refs/notes/commits`, `refs/notes/commits (delete)`, `refs/remotes/origin/lane-03`, `refs/remotes/origin/lane-03 (delete)`, `refs/remotes/origin/main`, `refs/remotes/origin/main (delete)` | `refs/aw/archive/x`, `refs/aw/archive/x (delete)`, `refs/heads/lane-03`, `refs/heads/lane-03 (delete)`, `refs/heads/lane-topic`, `refs/heads/lane-topic (delete)`, `refs/heads/main`, `refs/heads/main (delete)`, `refs/notes/commits`, `refs/notes/commits (delete)`, `refs/remotes/origin/lane-03`, `refs/remotes/origin/lane-03 (delete)`, `refs/remotes/origin/main`, `refs/remotes/origin/main (delete)` |
| lane | gc --auto | — | — | — | — |
| owner | commit | `HEAD`, `refs/heads/main` | `HEAD`, `refs/heads/main` | `HEAD`, `refs/heads/main` | `AUTO_MERGE (delete)`, `HEAD`, `refs/heads/main` |
| owner | commit --no-verify | `HEAD`, `refs/heads/main` | `HEAD`, `refs/heads/main` | `HEAD`, `refs/heads/main` | `AUTO_MERGE (delete)`, `HEAD`, `refs/heads/main` |
| owner | commit --amend | `HEAD`, `refs/heads/main` | `HEAD`, `refs/heads/main` | `HEAD`, `refs/heads/main` | `AUTO_MERGE (delete)`, `HEAD`, `refs/heads/main` |
| owner | branch create | `refs/heads/owner-branch` | `refs/heads/owner-branch` | `refs/heads/owner-branch` | `refs/heads/owner-branch` |
| owner | branch -f (other) | `refs/heads/owner-branch` | `refs/heads/owner-branch` | `refs/heads/owner-branch` | `refs/heads/owner-branch` |
| owner | branch -D (other) | `refs/heads/owner-branch (delete)` | `refs/heads/owner-branch (delete)` | `refs/heads/owner-branch (delete)` | `refs/heads/owner-branch (delete)` |
| owner | tag | `refs/tags/t-owner` | `refs/tags/t-owner` | `refs/tags/t-owner` | `refs/tags/t-owner` |
| owner | tag -d | `refs/tags/t-owner (delete)` | `refs/tags/t-owner (delete)` | `refs/tags/t-owner (delete)` | `refs/tags/t-owner (delete)` |
| owner | checkout -b | `refs/heads/owner-topic` | `refs/heads/owner-topic` | `refs/heads/owner-topic` | `AUTO_MERGE (delete)`, `HEAD`, `refs/heads/owner-topic` |
| owner | checkout back | — | — | — | `AUTO_MERGE (delete)`, `HEAD` |
| owner | switch --detach | `HEAD` | `HEAD` | `HEAD` | `AUTO_MERGE (delete)`, `HEAD` |
| owner | switch back | — | — | — | `AUTO_MERGE (delete)`, `HEAD` |
| owner | reset --hard HEAD~1 | `HEAD`, `ORIG_HEAD`, `refs/heads/main` | `HEAD`, `ORIG_HEAD`, `refs/heads/main` | `HEAD`, `ORIG_HEAD`, `refs/heads/main` | `AUTO_MERGE (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/main` |
| owner | stash push | `HEAD`, `ORIG_HEAD`, `refs/heads/main`, `refs/stash` | `HEAD`, `ORIG_HEAD`, `refs/heads/main`, `refs/stash` | `HEAD`, `ORIG_HEAD`, `refs/heads/main`, `refs/stash` | `AUTO_MERGE (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/main`, `refs/stash` |
| owner | stash pop | `refs/stash (delete)` | `refs/stash (delete)` | `refs/stash (delete)` | `AUTO_MERGE`, `refs/stash (delete)` |
| owner | stash drop (after push) | `HEAD`, `ORIG_HEAD`, `refs/heads/main`, `refs/stash`, `refs/stash (delete)` | `HEAD`, `ORIG_HEAD`, `refs/heads/main`, `refs/stash`, `refs/stash (delete)` | `HEAD`, `ORIG_HEAD`, `refs/heads/main`, `refs/stash`, `refs/stash (delete)` | `AUTO_MERGE (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/main`, `refs/stash`, `refs/stash (delete)` |
| owner | cherry-pick | `CHERRY_PICK_HEAD`, `CHERRY_PICK_HEAD (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/main` | `CHERRY_PICK_HEAD`, `CHERRY_PICK_HEAD (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/main` | `CHERRY_PICK_HEAD`, `CHERRY_PICK_HEAD (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/main` | `AUTO_MERGE`, `AUTO_MERGE (delete)`, `CHERRY_PICK_HEAD`, `CHERRY_PICK_HEAD (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/main` |
| owner | merge --ff-only main | `ORIG_HEAD` | `ORIG_HEAD` | `ORIG_HEAD` | `AUTO_MERGE (delete)`, `ORIG_HEAD` |
| owner | rebase main | `REBASE_HEAD (delete)` | `REBASE_HEAD (delete)` | `REBASE_HEAD (delete)` | `AUTO_MERGE (delete)`, `REBASE_HEAD (delete)` |
| owner | fetch | — | — | — | — |
| owner | push (own branch) | `refs/remotes/origin/main` | `refs/remotes/origin/main` | `refs/remotes/origin/main` | `refs/remotes/origin/main` |
| owner | update-ref other branch | `HEAD`, `refs/heads/main` | `HEAD`, `refs/heads/main` | `HEAD`, `refs/heads/main` | `refs/heads/main` |
| owner | update-ref refs/aw | `refs/aw/archive/x` | `refs/aw/archive/x` | `refs/aw/archive/x` | `refs/aw/archive/x` |
| owner | symbolic-ref HEAD | — | — | — | `HEAD` |
| owner | symbolic-ref back | `HEAD` | `HEAD` | `HEAD` | `AUTO_MERGE (delete)`, `HEAD` |
| owner | bisect start/reset | `HEAD` | `HEAD` | `HEAD` | `AUTO_MERGE (delete)`, `BISECT_EXPECTED_REV (delete)`, `BISECT_HEAD (delete)`, `HEAD`, `refs/bisect/bad`, `refs/bisect/bad (delete)`, `refs/bisect/good-<oid>`, `refs/bisect/good-<oid> (delete)` |
| owner | notes add | `refs/notes/commits` | `refs/notes/commits` | `refs/notes/commits` | `refs/notes/commits` |
| owner | pack-refs --all | `refs/aw/archive/x`, `refs/aw/archive/x (delete)`, `refs/heads/main`, `refs/heads/main (delete)`, `refs/heads/owner-topic`, `refs/heads/owner-topic (delete)`, `refs/notes/commits`, `refs/notes/commits (delete)`, `refs/remotes/origin/main`, `refs/remotes/origin/main (delete)` | `refs/aw/archive/x`, `refs/aw/archive/x (delete)`, `refs/heads/main`, `refs/heads/main (delete)`, `refs/heads/owner-topic`, `refs/heads/owner-topic (delete)`, `refs/notes/commits`, `refs/notes/commits (delete)`, `refs/remotes/origin/main`, `refs/remotes/origin/main (delete)` | `refs/aw/archive/x`, `refs/aw/archive/x (delete)`, `refs/heads/main`, `refs/heads/main (delete)`, `refs/heads/owner-topic`, `refs/heads/owner-topic (delete)`, `refs/notes/commits`, `refs/notes/commits (delete)`, `refs/remotes/origin/main`, `refs/remotes/origin/main (delete)` | `refs/aw/archive/x`, `refs/aw/archive/x (delete)`, `refs/heads/main`, `refs/heads/main (delete)`, `refs/heads/owner-topic`, `refs/heads/owner-topic (delete)`, `refs/notes/commits`, `refs/notes/commits (delete)`, `refs/remotes/origin/main`, `refs/remotes/origin/main (delete)` |
| owner | gc --auto | — | — | — | — |
| owner | worktree add (new branch) | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-04` | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-04` | `HEAD`, `ORIG_HEAD`, `refs/heads/lane-04` | `AUTO_MERGE (delete)`, `HEAD`, `ORIG_HEAD`, `refs/heads/lane-04` |
| owner | worktree remove | — | — | — | — |
| owner | branch -D lane-04 | `refs/heads/lane-04 (delete)` | `refs/heads/lane-04 (delete)` | `refs/heads/lane-04 (delete)` | `refs/heads/lane-04 (delete)` |


## Devcontainer CLI behavior for F5

Checked on 2026-11-12 by reading the source of `@devcontainers/cli` 0.89.0, the pinned version
(`dist/spec-node/devContainersSpecCLI.js`), for the 0.1.5 change requests' F5. Check 4 needs VS Code and is still open.

| Check | Finding |
|---|---|
| 1. Can a project's devcontainer set its own Compose name? (B2) | The CLI passes `--project-name`, resolved as: `COMPOSE_PROJECT_NAME` from its environment; else from the `.env` in its working directory; else the merged Compose configuration's top-level `name` (unless it is `devcontainer`), else the last listed file's; else `<folder>_devcontainer` when the files are in `.devcontainer`. Lowercased, and only `[-_a-z0-9]` kept. Reproduced with `docker compose config`: aw's old fragment, listed after the project's file, replaced its name. |
| 2. Does the CLI tolerate a listed Compose file that `initializeCommand` creates? (Mode B) | Yes. `up` runs `initializeCommand` (after a check of disallowed features, which reads no Compose file) before it reads the Compose configuration, so a file it creates is there in time. aw's `aw.compose.yaml` already relies on this. |
| 3. Does the CLI pass each Compose file with `-f`? | Yes: every file in `dockerComposeFile` becomes `-f <file>`, with `--project-name`. `COMPOSE_FILE` is read only when `dockerComposeFile` is absent. So Compose's automatic `compose.override.yaml` and `COMPOSE_FILE` never apply, and a setup with no project change cannot add aw's mount to a container VS Code builds from the committed configuration: that is why Mode B exists. |
| 4. Two configurations in one repository: does VS Code offer a picker, and does "Rebuild" reuse the configuration that built the container? (Mode A) | Not checked: it needs VS Code. |
