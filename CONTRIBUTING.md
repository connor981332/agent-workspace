# Contributing

Thanks for looking. Agent Workspace is a Go program, one binary with no daemon. These are the rules that keep it fast and safe.

## Before you open a pull request

```sh
gofmt -l internal acceptance
go vet ./...
go test ./...
(cd acceptance && go test ./...)
```

Behavior is specified by the scenarios in `acceptance/scenarios/`, which drive the `aw` binary from outside. A change in behavior comes with a scenario. Each scenario starts with `# req:`, `# source:` and `# milestone:` lines.

The real-Docker suite runs with `AW_E2E=1 go test -run TestE2E .` in `acceptance/`.

## Rules that are easy to break

- **Hot path.** `aw hook` and `aw git-hook` run on every tool call of every agent, and must stay within the budgets in `docs/spec/speed.md`. The packages on that path may not import `os/exec` or `net`; `internal/archtest` enforces it. A pull request that adds work there says why.
- **Guards fail closed.** If a guard cannot decide, the call is refused.
- **Errors name the fix.** An error says what is wrong, then `→`, then what to do.
- **A released migration is never edited.** A change to the state is a new migration, in `internal/state/migrate.go`.
- **No product content.** The framework ships mechanism: nothing specific to one team or product.
- **Portable shell.** Scenarios and the launcher run under `dash`, BusyBox and macOS's tools.

Run the benchmarks (`go run ./bench`, from `acceptance/`) on a quiet machine when you touch anything that a budget covers, and record the result in `docs/dev/benchmarks.md`.

Security issues: please open a private advisory on GitHub rather than a public issue. What the guards do and do not promise is in [docs/safety.md](docs/safety.md).
