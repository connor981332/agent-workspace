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
