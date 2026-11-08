# Benchmarks on developer laptops

The budgets in [speed.md](../spec/speed.md) are defined on a developer laptop. CI enforces them on
shared runners ([plan §5.2](../../plans/release-candidate.md#52-ci-enforcement)); twice per
milestone a maintainer runs the bench on a Linux laptop and on an Apple Silicon Mac and records the
results here:

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
