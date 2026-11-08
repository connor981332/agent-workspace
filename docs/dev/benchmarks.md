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

## M3

### Linux laptop, 2026-11-08

The same machine as M1, on power. aw built from the m3/mux branch, with every M3 guard in place.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.53 | 5.49 | 5.93 | 6.33 | 8.00 | pass |
| whoami | 500 | 3.91 | 4.77 | 5.25 | 6.74 | 10.00 | pass |
| config-get | 500 | 3.99 | 4.78 | 5.24 | 5.71 | 10.00 | pass |
| attach-from-pool | 100 | 35.13 | 38.96 | 40.88 | 48.16 | 100.00 | pass |
| new-until-harness | 100 | 37.15 | 40.52 | 43.88 | 44.22 | 150.00 | pass |
| dashboard | 200 | 45.08 | 48.29 | 50.31 | 52.69 | 100.00 | pass |
| ref-hook | 1000 | 4.21 | 5.03 | 5.59 | 6.49 | 8.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.

## M4

### Linux laptop, 2026-11-08

The same machine as M1, on power. aw built from the m4/exit branch.

| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |
|---|---|---|---|---|---|---|---|
| hook | 1000 | 4.80 | 5.85 | 6.41 | 7.11 | 8.00 | pass |
| whoami | 500 | 3.91 | 4.71 | 5.00 | 5.27 | 10.00 | pass |
| config-get | 500 | 3.95 | 4.87 | 5.37 | 6.19 | 10.00 | pass |
| attach-from-pool | 100 | 35.74 | 39.85 | 47.20 | 48.56 | 100.00 | pass |
| new-until-harness | 100 | 37.73 | 40.50 | 43.36 | 44.23 | 150.00 | pass |
| dashboard | 200 | 46.42 | 49.45 | 50.41 | 52.14 | 100.00 | pass |
| ref-hook | 1000 | 4.40 | 5.21 | 5.66 | 6.45 | 8.00 | pass |
| serve-free-seat | 100 | 7.51 | 8.40 | 9.07 | 9.24 | 50.00 | pass |
| grant-next | 100 | 5.20 | 6.19 | 6.44 | 6.45 | 50.00 | pass |
| preempt | 100 | 6.76 | 7.56 | 7.88 | 8.74 | 50.00 | pass |

### Apple Silicon laptop

Not yet measured: pending, for a maintainer with a Mac.
