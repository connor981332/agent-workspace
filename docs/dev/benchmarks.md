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
