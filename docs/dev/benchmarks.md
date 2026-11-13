# Benchmarks on developer laptops

The budgets in [speed.md](../spec/speed.md) are defined on a developer laptop, and enforced here. CI
looks for regressions against master ([plan §5.2](../../plans/release-candidate.md#52-ci-enforcement)).
At each milestone exit and before each release, a maintainer runs the bench on a Linux laptop and on
an Apple Silicon Mac, with the machine idle and on power, and records the results here. The release
workflow refuses a tag whose milestone has no Linux laptop result:

```sh
cd acceptance && AW_BENCH_HOST="<machine>" go run ./bench
```

## M1

### Linux laptop, 2026-11-07

Intel Core Ultra X7 358H, Arch Linux, `/bin/sh` is bash, cpufreq governor powersave, platform profile
"performance". aw built from master after PR #15.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 3.60 | 4.19 | 4.48 | 5.35 | 8.00 | pass |
| whoami | 500 | 3.39 | 3.90 | 4.10 | 5.00 | 10.00 | pass |
| config-get | 500 | 3.44 | 3.97 | 4.34 | 5.06 | 10.00 | pass |

The hook includes the launcher, which runs the binary as a child for hooks (so that a binary that
cannot start still refuses), the payload decode, the config cache and a read-only SQLite open.

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M2

### Linux laptop, 2026-11-08

The same machine as M1. aw built from the m2/dashboard branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 3.98 | 4.69 | 5.07 | 5.37 | 8.00 | pass |
| whoami | 500 | 3.40 | 3.94 | 4.35 | 4.88 | 10.00 | pass |
| config-get | 500 | 3.42 | 4.06 | 4.37 | 4.49 | 10.00 | pass |
| dashboard | 200 | 42.20 | 44.12 | 45.73 | 46.47 | 100.00 | pass |
| attach-from-pool | 100 | 32.11 | 34.09 | 35.67 | 36.42 | 100.00 | pass |
| new-until-harness | 100 | 33.68 | 36.05 | 38.50 | 38.84 | 150.00 | pass |

The dashboard reads ten lanes of five repositories each (the workspace repository and four
projects) with two Git calls per repository. The first version ran the merge proof in every clean
repository and took 80 ms here; it now proves only lanes whose repositories are all clean, one
repository at a time, stopping at the first that is not proven.

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## A correction to earlier results

Until 2026-11-08 the bench kept its fixture in the system's temporary directory, which on this machine
is a RAM disk, where a sync costs nothing. The M1 and M2 results above, and the first M3 results, were
measured that way. The bench now uses the user's cache directory, on the real disk.

On a real disk, deleting and recreating SQLite's write-ahead log for each command cost about 27 ms, and
`grant-next` missed its budget at 53 ms p99. Writers now keep the log between commands and compact it
at 4 MB, and read-only connections open with `mode=ro`, so they cannot touch it. With the log at 4 MB,
opening the state for a read costs about 1.5 ms more, which keeps the guard hook within its budget.

## M3

### Linux laptop, 2026-11-08

The same machine as M1, on power, on its NVMe disk (btrfs), the machine mostly idle. aw built from the m4/exit branch. Measured again after the correction above; v0.0.4 was released on the RAM-disk result.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.18 | 4.78 | 5.09 | 5.69 | 8.00 | pass |
| whoami | 500 | 3.38 | 3.83 | 4.04 | 4.46 | 10.00 | pass |
| config-get | 500 | 3.33 | 3.79 | 4.12 | 4.45 | 10.00 | pass |
| attach-from-pool | 100 | 63.61 | 73.32 | 76.27 | 76.75 | 100.00 | pass |
| new-until-harness | 100 | 65.78 | 76.01 | 89.00 | 95.25 | 150.00 | pass |
| dashboard | 200 | 43.47 | 44.90 | 45.26 | 45.85 | 100.00 | pass |
| ref-hook | 1000 | 4.64 | 5.26 | 5.64 | 6.68 | 8.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M4

### Linux laptop, 2026-11-08

The same machine as M1, on power, on its NVMe disk (btrfs), the machine mostly idle. aw built from the m4/exit branch. 

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.18 | 4.78 | 5.09 | 5.69 | 8.00 | pass |
| whoami | 500 | 3.38 | 3.83 | 4.04 | 4.46 | 10.00 | pass |
| config-get | 500 | 3.33 | 3.79 | 4.12 | 4.45 | 10.00 | pass |
| attach-from-pool | 100 | 63.61 | 73.32 | 76.27 | 76.75 | 100.00 | pass |
| new-until-harness | 100 | 65.78 | 76.01 | 89.00 | 95.25 | 150.00 | pass |
| dashboard | 200 | 43.47 | 44.90 | 45.26 | 45.85 | 100.00 | pass |
| ref-hook | 1000 | 4.64 | 5.26 | 5.64 | 6.68 | 8.00 | pass |
| serve-free-seat | 100 | 7.73 | 8.89 | 32.68 | 43.81 | 50.00 | pass |
| grant-next | 100 | 26.70 | 42.18 | 44.85 | 44.93 | 50.00 | pass |
| preempt | 100 | 7.11 | 7.95 | 8.32 | 27.85 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M5

### Linux laptop, 2026-11-08

The same machine as M1, on power, on its NVMe disk (btrfs), load average about 1. aw built from the m5/providers branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.22 | 4.73 | 5.12 | 6.35 | 8.00 | pass |
| whoami | 500 | 3.42 | 3.96 | 4.45 | 4.68 | 10.00 | pass |
| config-get | 500 | 3.38 | 3.94 | 4.26 | 4.58 | 10.00 | pass |
| attach-from-pool | 100 | 56.63 | 68.60 | 69.75 | 72.13 | 100.00 | pass |
| new-until-harness | 100 | 67.66 | 76.12 | 79.03 | 84.49 | 150.00 | pass |
| dashboard | 200 | 43.62 | 45.37 | 46.10 | 46.36 | 100.00 | pass |
| ref-hook | 1000 | 4.65 | 5.24 | 5.71 | 6.99 | 8.00 | pass |
| serve-free-seat | 100 | 7.93 | 9.16 | 37.76 | 40.85 | 50.00 | pass |
| grant-next | 100 | 26.95 | 44.74 | 45.44 | 45.84 | 50.00 | pass |
| preempt | 100 | 7.32 | 8.13 | 8.56 | 27.56 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M6

### Linux laptop, 2026-11-08

The same machine as M1, on power, on its NVMe disk (btrfs), load average about 3 when it started. aw built from the m6/runner branch. Commands that wait on Docker have no fixed budget (speed.md); these operations are on a host-runner project.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.31 | 4.83 | 5.24 | 6.38 | 8.00 | pass |
| whoami | 500 | 3.45 | 3.97 | 4.17 | 4.69 | 10.00 | pass |
| config-get | 500 | 3.45 | 3.88 | 4.05 | 4.24 | 10.00 | pass |
| attach-from-pool | 100 | 65.85 | 72.53 | 73.92 | 105.74 | 100.00 | pass |
| new-until-harness | 100 | 57.70 | 68.62 | 70.68 | 73.66 | 150.00 | pass |
| dashboard | 200 | 43.68 | 45.28 | 46.50 | 48.21 | 100.00 | pass |
| ref-hook | 1000 | 4.69 | 5.29 | 5.88 | 6.55 | 8.00 | pass |
| serve-free-seat | 100 | 7.96 | 9.17 | 29.04 | 37.45 | 50.00 | pass |
| grant-next | 100 | 26.50 | 36.57 | 38.48 | 38.50 | 50.00 | pass |
| preempt | 100 | 7.12 | 8.24 | 8.52 | 27.27 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M7

### Linux laptop, 2026-11-08

The same machine as M1, on power, on its NVMe disk (btrfs), load average about 3 when it started. aw built from the m7/exit branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.30 | 4.84 | 5.21 | 6.24 | 8.00 | pass |
| whoami | 500 | 3.42 | 3.83 | 4.13 | 4.49 | 10.00 | pass |
| config-get | 500 | 3.35 | 3.86 | 4.09 | 4.39 | 10.00 | pass |
| attach-from-pool | 100 | 61.29 | 70.92 | 74.45 | 91.29 | 100.00 | pass |
| new-until-harness | 100 | 57.74 | 73.06 | 75.92 | 80.71 | 150.00 | pass |
| dashboard | 200 | 43.42 | 45.12 | 45.96 | 46.61 | 100.00 | pass |
| ref-hook | 1000 | 4.69 | 5.18 | 5.51 | 6.39 | 8.00 | pass |
| serve-free-seat | 100 | 8.02 | 9.22 | 35.70 | 43.58 | 50.00 | pass |
| grant-next | 100 | 27.07 | 40.60 | 44.86 | 45.24 | 50.00 | pass |
| preempt | 100 | 7.16 | 8.23 | 8.73 | 30.93 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M8

### Linux laptop, 2026-11-09

The same machine as M1, on power, on its NVMe disk (btrfs), load average about 3 when it started. aw built from the m8/upgrades branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.27 | 4.82 | 5.23 | 5.95 | 8.00 | pass |
| whoami | 500 | 3.47 | 4.00 | 4.33 | 4.99 | 10.00 | pass |
| config-get | 500 | 3.38 | 3.90 | 4.19 | 4.57 | 10.00 | pass |
| attach-from-pool | 100 | 61.41 | 73.79 | 82.12 | 87.82 | 100.00 | pass |
| new-until-harness | 100 | 58.00 | 69.99 | 77.53 | 78.92 | 150.00 | pass |
| dashboard | 200 | 43.64 | 45.27 | 46.62 | 47.19 | 100.00 | pass |
| ref-hook | 1000 | 4.65 | 5.11 | 5.45 | 6.23 | 8.00 | pass |
| serve-free-seat | 100 | 8.10 | 9.03 | 27.11 | 29.85 | 50.00 | pass |
| grant-next | 100 | 28.19 | 43.98 | 44.44 | 45.16 | 50.00 | pass |
| preempt | 100 | 7.21 | 8.14 | 8.76 | 27.33 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M9

### Linux laptop, 2026-11-09

The same machine as M1, on power, on its NVMe disk (btrfs), load average about 3 when it started. aw built from the m9/owner branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.37 | 4.90 | 5.24 | 5.74 | 8.00 | pass |
| whoami | 500 | 3.51 | 3.90 | 4.17 | 4.37 | 10.00 | pass |
| config-get | 500 | 3.47 | 4.01 | 4.31 | 5.36 | 10.00 | pass |
| attach-from-pool | 100 | 60.12 | 75.28 | 76.20 | 101.15 | 100.00 | pass |
| new-until-harness | 100 | 58.59 | 73.07 | 79.40 | 85.30 | 150.00 | pass |
| dashboard | 200 | 43.38 | 45.43 | 46.26 | 47.07 | 100.00 | pass |
| ref-hook | 1000 | 4.68 | 5.17 | 5.43 | 5.98 | 8.00 | pass |
| serve-free-seat | 100 | 7.99 | 9.03 | 29.37 | 36.20 | 50.00 | pass |
| grant-next | 100 | 30.09 | 45.23 | 45.43 | 46.28 | 50.00 | pass |
| preempt | 100 | 7.25 | 8.29 | 8.63 | 37.95 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M10

### Linux laptop, 2026-11-09

The same machine as M1, on power, on its NVMe disk (btrfs). aw built from the m10/modules branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.43 | 5.02 | 5.23 | 5.85 | 8.00 | pass |
| whoami | 500 | 3.55 | 4.14 | 4.53 | 5.44 | 10.00 | pass |
| config-get | 500 | 3.54 | 4.02 | 4.33 | 4.78 | 10.00 | pass |
| attach-from-pool | 100 | 59.74 | 74.62 | 77.12 | 102.46 | 100.00 | pass |
| new-until-harness | 100 | 61.36 | 74.66 | 78.67 | 80.61 | 150.00 | pass |
| dashboard | 200 | 43.67 | 45.38 | 45.88 | 46.52 | 100.00 | pass |
| ref-hook | 1000 | 4.71 | 5.39 | 5.83 | 6.70 | 8.00 | pass |
| serve-free-seat | 100 | 8.16 | 9.19 | 30.16 | 41.20 | 50.00 | pass |
| grant-next | 100 | 27.58 | 37.28 | 44.46 | 51.58 | 50.00 | pass |
| preempt | 100 | 7.25 | 8.26 | 10.62 | 28.55 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M11

### Linux laptop, 2026-11-09

The same machine as M1, on power, on its NVMe disk (btrfs). aw built from the m11/onboarding branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.44 | 5.04 | 5.40 | 5.77 | 8.00 | pass |
| whoami | 500 | 3.63 | 4.12 | 4.35 | 4.62 | 10.00 | pass |
| config-get | 500 | 3.62 | 4.07 | 4.30 | 4.45 | 10.00 | pass |
| attach-from-pool | 100 | 57.52 | 72.08 | 75.60 | 75.81 | 100.00 | pass |
| new-until-harness | 100 | 60.75 | 74.02 | 77.39 | 83.53 | 150.00 | pass |
| dashboard | 200 | 43.84 | 45.46 | 46.31 | 47.26 | 100.00 | pass |
| ref-hook | 1000 | 4.84 | 5.32 | 5.62 | 6.08 | 8.00 | pass |
| serve-free-seat | 100 | 8.21 | 9.30 | 33.78 | 40.86 | 50.00 | pass |
| grant-next | 100 | 27.05 | 38.78 | 42.97 | 45.00 | 50.00 | pass |
| preempt | 100 | 7.40 | 8.17 | 8.46 | 27.33 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M12

### Linux laptop, 2026-11-09

The same machine as M1, on power, on its NVMe disk (btrfs). aw built from the m12/final branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.54 | 5.15 | 5.62 | 6.27 | 8.00 | pass |
| whoami | 500 | 3.63 | 4.05 | 4.38 | 5.16 | 10.00 | pass |
| config-get | 500 | 3.63 | 4.09 | 4.37 | 4.79 | 10.00 | pass |
| attach-from-pool | 100 | 66.86 | 74.52 | 77.79 | 78.07 | 100.00 | pass |
| new-until-harness | 100 | 57.66 | 70.04 | 76.00 | 77.42 | 150.00 | pass |
| dashboard | 200 | 43.82 | 45.52 | 46.21 | 47.02 | 100.00 | pass |
| ref-hook | 1000 | 4.89 | 5.37 | 5.80 | 6.96 | 8.00 | pass |
| serve-free-seat | 100 | 8.28 | 9.26 | 28.89 | 36.26 | 50.00 | pass |
| grant-next | 100 | 27.20 | 43.48 | 45.12 | 46.73 | 50.00 | pass |
| preempt | 100 | 7.44 | 8.16 | 8.57 | 28.63 | 50.00 | pass |

### Linux laptop, 2026-11-12, lane attribution fix

The same machine, not quiet (load average about 2). aw built from fix/lane-attribution. The dashboard
costs about 10 ms more at p50 than on 2026-11-09: recap now asks each repository without an upstream
whether any remote has its head, and counts a lane's commits against its starting commit as well as
its base. Claims now read origin's default branch and how the owner checkout differs from it.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.66 | 5.32 | 5.62 | 6.74 | 8.00 | pass |
| whoami | 500 | 3.76 | 4.26 | 4.48 | 4.61 | 10.00 | pass |
| config-get | 500 | 3.73 | 4.19 | 4.54 | 5.00 | 10.00 | pass |
| attach-from-pool | 100 | 65.48 | 75.95 | 81.01 | 85.66 | 100.00 | pass |
| new-until-harness | 100 | 67.74 | 80.80 | 99.40 | 107.91 | 150.00 | pass |
| dashboard | 200 | 53.74 | 56.13 | 56.84 | 58.06 | 100.00 | pass |
| ref-hook | 1000 | 5.06 | 5.58 | 6.15 | 6.59 | 8.00 | pass |
| serve-free-seat | 100 | 8.50 | 9.53 | 31.33 | 41.43 | 50.00 | pass |
| grant-next | 100 | 34.41 | 44.66 | 46.33 | 55.64 | 50.00 | pass |
| preempt | 100 | 7.68 | 8.49 | 8.88 | 37.38 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

### Linux laptop, 2026-11-12, devcontainer Mode B

Measured on `connor-fw13`, Linux amd64, branch `feedback/7-devcontainer` at `14b4e35`.
The machine was not quiet (load averaged roughly 2–5 during the run); these are diagnostic
measurements, not a quiet-machine milestone certification. Config adds a cached string and load-time
path validation; guard decisions add no runner work.

Full absolute run: `TMPDIR=~/.cache/aw-tmp GOTMPDIR=~/.cache/aw-tmp go run ./bench`.
Seven operations exceeded their absolute budgets, including hook and ref-hook, after the harness's
built-in retries. A subsequent interleaved comparison against master (`715df80`), built with the same
`CGO_ENABLED=0`, trimpath and benchmark version flags, passed every relative regression check at
`-mode relative -scale 0.1`. The reduced comparison sample does not certify absolute budgets.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 12.46 | 13.63 | 14.24 | 15.03 | 8.00 | fail |
| whoami | 500 | 9.76 | 10.64 | 11.02 | 11.53 | 10.00 | fail |
| config-get | 500 | 9.75 | 10.82 | 11.66 | 12.67 | 10.00 | fail |
| attach-from-pool | 100 | 134.81 | 144.48 | 153.19 | 154.66 | 100.00 | fail |
| new-until-harness | 100 | 141.58 | 155.65 | 165.31 | 179.02 | 150.00 | fail |
| dashboard | 200 | 85.15 | 93.61 | 98.22 | 104.23 | 100.00 | pass |
| ref-hook | 1000 | 11.62 | 12.94 | 13.69 | 14.62 | 8.00 | fail |
| serve-free-seat | 100 | 23.63 | 27.27 | 28.86 | 49.35 | 50.00 | pass |
| grant-next | 100 | 42.35 | 59.00 | 61.72 | 63.25 | 50.00 | fail |
| preempt | 100 | 20.63 | 24.69 | 30.00 | 42.24 | 50.00 | pass |

Interleaved comparison (p50, at most 25% above master):

| Operation | n | branch ms | master ms | result |
|---|---|---|---|---|
| hook | 100 | 12.78 | 12.87 | pass |
| whoami | 50 | 10.13 | 9.98 | pass |
| config-get | 50 | 10.09 | 10.20 | pass |
| attach-from-pool | 10 | 122.86 | 125.39 | pass |
| new-until-harness | 10 | 137.68 | 141.10 | pass |
| dashboard | 20 | 86.96 | 86.95 | pass |
| ref-hook | 100 | 13.77 | 13.83 | pass |
| serve-free-seat | 10 | 21.83 | 22.43 | pass |
| grant-next | 10 | 47.30 | 50.29 | pass |
| preempt | 10 | 17.86 | 17.73 | pass |

The completed shared F5 groundwork (`5945b15`, including config-hash replacement and process
inspection) was compared again with the same master baseline at `-mode relative -scale 0.1`:
all ten regression checks passed. Hook p50 was 12.91 ms versus master's 12.91 ms; ref-hook was
13.97 ms versus 13.83 ms. These remain non-quiet, reduced-sample relative results; the absolute
failures above are not cleared by them.

### Linux laptop, 2026-11-12, generated devcontainer configuration

`feedback/9-devcontainer-overlay` (Mode A), compared interleaved with the owner's `aw-dev`
built from merged master `21b2e79`, on `connor-fw13` (Linux amd64). Command:
`TMPDIR=~/.cache/aw-tmp GOTMPDIR=~/.cache/aw-tmp go run ./bench -mode relative -scale 0.1 -baseline-aw ~/.local/bin/aw-dev`.
All relative regression checks passed. Guard decisions add no runner work; config changes only its
runner description. Reduced-sample comparisons do not certify quiet-machine absolute budgets.

| Operation | n | branch p50 ms | master p50 ms | result |
|---|---|---|---|---|
| hook | 100 | 5.46 | 5.92 | pass |
| whoami | 50 | 4.52 | 4.87 | pass |
| config-get | 50 | 4.53 | 4.85 | pass |
| attach-from-pool | 10 | 71.04 | 71.72 | pass |
| new-until-harness | 10 | 78.97 | 80.11 | pass |
| dashboard | 20 | 56.18 | 57.26 | pass |
| ref-hook | 100 | 5.98 | 6.42 | pass |
| serve-free-seat | 10 | 9.39 | 10.01 | pass |
| grant-next | 10 | 29.02 | 29.30 | pass |
| preempt | 10 | 6.93 | 7.74 | pass |
