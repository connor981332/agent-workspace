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
measured that way. The bench now uses the user's cache directory, on the real disk. On a real disk a
state-changing command pays two syncs, about 13 ms each: one when SQLite starts a new write-ahead log,
and one when it folds the log back into the database as the last connection closes.

## M3

### Linux laptop, 2026-11-08

The same machine as M1, on power, on its NVMe disk (btrfs), with other work running (load average
about 3). aw built from the m4/exit branch. Measured again after the correction above; v0.0.4 was released on the RAM-disk result, and every M3
budget also holds on the disk.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.48 | 5.32 | 5.82 | 6.83 | 8.00 | pass |
| whoami | 500 | 3.54 | 4.19 | 4.41 | 4.92 | 10.00 | pass |
| config-get | 500 | 3.53 | 4.17 | 4.49 | 4.72 | 10.00 | pass |
| attach-from-pool | 100 | 67.98 | 84.00 | 88.97 | 93.27 | 100.00 | pass |
| new-until-harness | 100 | 74.78 | 88.11 | 104.12 | 111.08 | 150.00 | pass |
| dashboard | 200 | 47.31 | 55.88 | 65.70 | 66.98 | 100.00 | pass |
| ref-hook | 1000 | 3.96 | 4.74 | 5.78 | 6.54 | 8.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M4

### Linux laptop, 2026-11-08

The same machine as M1, on power, on its NVMe disk (btrfs), with other work running (load average
about 3). aw built from the m4/exit branch. `grant-next` misses its budget here, so the release workflow will refuse an M4 tag on this result.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.48 | 5.32 | 5.82 | 6.83 | 8.00 | pass |
| whoami | 500 | 3.54 | 4.19 | 4.41 | 4.92 | 10.00 | pass |
| config-get | 500 | 3.53 | 4.17 | 4.49 | 4.72 | 10.00 | pass |
| attach-from-pool | 100 | 67.98 | 84.00 | 88.97 | 93.27 | 100.00 | pass |
| new-until-harness | 100 | 74.78 | 88.11 | 104.12 | 111.08 | 150.00 | pass |
| dashboard | 200 | 47.31 | 55.88 | 65.70 | 66.98 | 100.00 | pass |
| ref-hook | 1000 | 3.96 | 4.74 | 5.78 | 6.54 | 8.00 | pass |
| serve-free-seat | 100 | 21.30 | 30.46 | 33.67 | 42.15 | 50.00 | pass |
| grant-next | 100 | 32.91 | 52.48 | 53.69 | 57.71 | 50.00 | **FAIL** (; failed twice (first run p50 32.67, p99 54.51 ms)) |
| preempt | 100 | 21.01 | 28.85 | 30.08 | 30.85 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.
