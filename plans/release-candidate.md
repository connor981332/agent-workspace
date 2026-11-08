# Release candidate implementation plan

How to build the Agent Workspace release candidate: one Go binary, `aw`, which replaces
`workspace-candidate-1` and `workspace-candidate-2` and will be published as open source.

The design is settled in [docs/spec/](../docs/spec/README.md). This plan does not redesign it. It
orders the work, defines the test suites that prove each step, and lists the places where the spec
is ambiguous or incomplete ([Spec issues](#spec-issues)). Each of those needs an answer before the
milestone named in its row.

## Status

**State:** PARTIALLY IMPLEMENTED

**Updated:** 2026-11-07

**Branches / MRs:** PRs [#5](https://github.com/connor981332/agent-workspace/pull/5) to
[#15](https://github.com/connor981332/agent-workspace/pull/15), merged: the plan and settled
questions (#5, #6), M0 (#7 to #11), M1 (#12 to #16), M2 lanes (#17) and sweep (#18). `m2/forge` (M2 forge proof, open).
`spike/hot-path` (T3 and the ref-hook spike, never merged).

**Done:** Read the spec and both candidates and wrote this plan. Settled the spec's five open
questions (§8). The spec owner accepted every proposed answer in [Spec issues](#spec-issues) (S1 to
S19) on 2026-11-07; each is recorded in the spec by the PR of the milestone that needs it (S5 and S6
are recorded).
- **M0 complete.** The Go module and CI matrix, the acceptance harness, GoReleaser with a
  reproducible snapshot build, the PR template with the hot-path check, and the hot-path and
  ref-hook spikes ([docs/dev/spikes.md](../docs/dev/spikes.md)). T3 missed its 3 ms target; the spec
  owner kept the design and set the guard budget to 8 ms p99 (M0 target 6 ms, met at 5.58 ms on
  ubuntu-24.04), with macOS CI checked for regressions only.
- **M1 complete.** Configuration: three layers, locks, provenance, strict errors
  with file, line and fix, the compiled cache, `aw config get|explain|validate`, and the reference
  generated from the schema. State: SQLite in WAL mode with versioned migrations, backups, and
  read-only mode. The `bin/aw` launcher (exact pins, checksums, a local mirror, and hooks that fail
  closed) and `aw init`. `aw whoami`, `aw doctor`, the hook stub, timings, NO_COLOR and
  control-character stripping. All 27 M1 scenarios pass, and the bench enforces the M1 budgets in
  CI. Decisions made with the spec owner during M1: §4.3 rule 3 allowlists `modernc.org/libc` and
  `github.com/google/uuid` (needed by the SQLite driver). Changes to the plan's mechanics:
  `requirements.toml` gained `in_progress`, so a milestone's written scenarios and benchmarks run
  in CI from the PR that adds them.

**In progress:** M2. The M2 rows of §3.5 are pending scenario files, with seven rows that need
hooks, seats or `aw open` moved to M3 and M4. S4 and S17 are recorded in the spec. Done: state
schema 2, lane names from purpose, `aw attach` with worktrees, links, the owner pointer, secret
links, lane guards, the lane limit, `--branch` and the pool (PR #17). Local merge proof (ancestry,
patch equivalence), `aw sweep [--apply] [--retry]` with the warm pool, retention and a journal,
`aw discard` with archived refs, `aw park`/`unpark`, and `aw finish` with handoffs (PR #18). Now
(`m2/forge`): forge exact-head proof through `gh` and `glab`. 51 of the 73 M2 scenarios are
written.

**Next step:** sessions (liveness, interruption, handoff reading),
`aw new`, `aw run`, the dashboard and `aw recap`, the M2 budgets, then the first pre-release.

**Pending manual checks:** the hot-path and budget benchmarks on an Apple Silicon laptop (T3, M1;
docs/dev/benchmarks.md).

## How to use this plan

- Follow the milestones in order. A milestone is done only when its exit criterion passes in CI on
  all four platforms (Linux and macOS, amd64 and arm64), except where the criterion names a manual
  check.
- Before starting a milestone, get the spec owner's answer to every spec issue whose "Needed by"
  column names that milestone, and record it in the spec, not only here.
- Requirement numbers (R1 to R94) refer to [requirements.md](../docs/spec/requirements.md). "C1"
  means `workspace-candidate-1` (Python) and "C2" means `workspace-candidate-2` (shell). "F*n*"
  means finding *n* in [C2's findings.md](../workspace-candidate-2/docs/findings.md).
- Acceptance scenarios are written **before or together with** the code they cover. A pull request
  that adds behavior also adds or enables the scenarios for it.
- Keep the Status block above up to date. Its fields follow the plan convention from
  [C1's docs/plans.md](../workspace-candidate-1/docs/plans.md) (R82). The State is one of `DRAFT`,
  `PLANNED`, `PARTIALLY IMPLEMENTED`, `FULLY IMPLEMENTED` or `FULLY VERIFIED`, and the row in
  [plans/README.md](README.md) must agree with it.

## Contents

1. [Milestones](#1-milestones)
2. [Traceability: R1 to R94](#2-traceability-r1-to-r94)
3. [Acceptance suite](#3-acceptance-suite)
4. [Go module layout](#4-go-module-layout)
5. [Latency benchmarks](#5-latency-benchmarks)
6. [Release pipeline](#6-release-pipeline)
7. [CI](#7-ci)
8. [Open questions](#8-open-questions)
9. [Decisions this plan makes](#9-decisions-this-plan-makes)
10. [Risks](#10-risks)
11. [Spec issues](#spec-issues)
12. [First tasks](#12-first-tasks)

---

## 1. Milestones

Each milestone is a shippable increment. From M2 on, every milestone exit before v0.1 is tagged as
a pre-release `v0.0.N`, where N is the milestone number (M2 gives `v0.0.2`), so the maintainers can
dogfood it in their own workspaces. The two candidates stay usable throughout.

### Why v0.1 is where it is

**v0.1 is the first version a team could use every day instead of either candidate.** It ships at
the end of M7. Both workspaces the candidates came from run their projects in containers, rely on
per-lane databases, share scarce servers, and (in C1's case) enforce lanes with hooks. A release
without any one of these would be a step back for one of those teams. So v0.1 includes config
layers, the state store, the launcher, lanes, merge proof with forge evidence, guards for Claude
Code, Codex, Grok and OpenCode, the resource queue with owner preemption, lifecycle hooks with the Postgres and
Redis providers, the host and devcontainer runners, and a published, verifiable release.

**Deferred past v0.1, and why:**

| Deferred | Milestone | Why it can wait |
|---|---|---|
| `aw upgrade`, config and state migration, managed-file edit detection, Homebrew | M8 (v0.2) | v0.1 has one config schema and one state schema, and patch releases may not change either (distribution.md). v0.1.x pins are bumped by editing `.aw/version` and `.aw/lock`, a documented manual step. The managed-file hashes are written from v0.1 on, so M8 can detect edits made before it. |
| Delegation, roles, publication pre-authorization, change sets, `move-to-lane`, `try` | M9 (v0.3) | They make the tool more convenient but no safer. v0.1 already asks before anything outward-facing (the default in R72), and the owner can run maintenance commands personally. |
| Modules, shared-infrastructure management, stand-ins, per-session browser, the plans mechanism | M10 (v0.4) | Teams keep their existing Compose files and plans as their own files. Lanes do not depend on them. |
| `aw inspect`, `aw rehearse`, onboarding skills, `aw add project` | M11 (v0.5) | Maintainers write `workspace.toml` by hand, as they do with both candidates today. |
| Documentation for outsiders, declarative harness adapters, the optional supervisor, the security review | M12 (1.0) | These are about the public release, not about daily use. The four built-in adapters already cover the harnesses the maintainers use. |

### M0: Foundations

Builds the scaffolding that every later milestone depends on, and answers the two technical
questions that could force a spec change (hot-path latency and Git's ref hook).

- Go module at the repository root, `cmd/aw`, and an `aw version --json` command. See
  [T1](#t1-go-module-skeleton-and-ci-matrix).
- The acceptance harness (§3) with its stubs and its first scenarios, running on all four platforms.
  See [T2](#t2-acceptance-harness-skeleton).
- The benchmark harness (§5), which measures a prototype hot path. See
  [T3](#t3-hot-path-latency-spike).
- **Ref-hook spike:** a script that records which Git commands fire `reference-transaction`, with
  which ref names and states, from which worktree, on Git 2.34, 2.39 (Apple Git) and the latest
  release. Its results set the minimum Git version and the allowed-ref rules (S3).
- GoReleaser configuration, checked and built as a snapshot in CI (§6).
- A pull request template with a "Hot path" section, and the CI check that requires it (§5).

**Exit criterion:** CI passes on all four platforms. The acceptance harness runs at least three
scenarios. The prototype hot path (the launcher, then the binary, which opens SQLite, reads one
row, decodes a config cache and parses one shell command) measures **under 6 ms p99** on the Linux
reference runner. That leaves 2 ms of the 8 ms budget for the real rules. The ref-hook behavior
matrix is recorded in `docs/dev/spikes.md`. If the target is missed, stop and take the fallback
options in risk 1 to the spec owner before starting M1. (The target was 3 ms of a 5 ms budget
until the spike missed it; see risk 1.)

### M1: Configuration, state and launcher

- `config`: three layers (R6), team locks with refused overrides reported (R7), `aw config get`,
  `aw config explain` with file and line provenance (R8), strict decoding whose errors name the
  file, the line and the fix (R9), the schema version key, and the compiled cache keyed by source
  metadata.
- `state`: SQLite in WAL mode, `PRAGMA user_version`, a migration runner (with no migrations yet),
  the backup-before-migrate step, and **read-only mode for a newer or unknown schema** (R19).
  Records that cannot be read protect what they describe.
- `bin/aw` launcher: written to pass under dash, bash, BusyBox ash and macOS `/bin/sh`. It reads
  `.aw/owner` in a lane root, `.aw/version` and `.aw/lock`, and execs the cached binary. On a cache
  miss it fetches from `AW_MIRROR` (in this milestone, the test harness's local mirror), verifies
  the checksum and stores the binary atomically (R15, R16, R17). It honors `AW_TOOL_PATH`.
- `aw init` (minimal): writes `workspace.toml`, `.aw/version`, `.aw/lock`, `.aw/managed`, `bin/aw`
  and the `.gitignore` entries. It refuses an existing workspace.
- `aw whoami`, `aw doctor` (read-only, works before setup, every failure names its fix: R14),
  `--json` everywhere and the exit-code table (R93).
- A hook stub, `aw hook <harness> <event>`, that loads the cache, opens state read-only and returns
  allow. It exists so that the 8 ms guard budget is enforced from M1, before real rules arrive.
- Debug phase timings, and `aw doctor --timings`.

**Exit criterion:** the M1 scenarios pass (the `config/`, `state/`, `launcher/`, `init/`, `doctor/`
and `cli/` areas in §3.5). CI enforces these budgets (§5.2): `aw whoami` and `aw config get` under 10 ms
p99, and the hook stub under 8 ms p99.

### M2: Lanes and merge proof

- `lanes`: numbered, stable lane directories; names derived from purpose (R22); worktrees inside
  each project (`<project>/.worktrees/NN`) with relative links from the lane root; single-repository
  workspaces; `lanes = "shared"` projects; lane guards checked before anything is created (R28);
  secret links that are relative, read-only and excluded from Git (R67, F10); and the lane limit
  (R46, its lane half).
- Commands: `aw attach "<purpose>"` (session identity from explicit flags and environment in this
  milestone; hook-based identity comes in M3), `aw new` (launches a stub harness, passing the
  harness's arguments through unchanged), `aw run <project> …` on the host runner (argv, standard
  input, exit codes and signals passed through), `aw finish`, `aw park` and `aw unpark`,
  `aw discard` (archives branches under `refs/aw/archive/` before removing anything: R27), and
  `aw sweep` with and without `--apply`.
- `evidence`: ancestry, patch equivalence, and forge exact-head proof through `gh` and `glab`
  (R71, the proof half), with "unknown" whenever the forge cannot be read. Forge queries have a
  deadline and run only on `--remote` and in `aw sweep`.
- Warm pool: recycle, retention and pool size (R26). Claiming a pooled lane must be near-instant
  (R24). Cleanup and teardown are journaled and can be retried.
- `sessions`: the session record, its generation check, interrupted detection after a reboot or
  crash (R80, the session half), and the handoff record (R78).
- The `aw` dashboard and `aw recap`, local only (R81).
- First pre-release, `v0.0.2`, through the release workflow (§6).

**Safety invariants covered in this milestone:** merge proof (nothing unproven is removed);
unknown means protected (an unreadable state, a lane marker that does not match, a locked
worktree, a detached HEAD, a session lock that cannot be read); the owner checkout is never
disturbed by `attach`, `new`, `sweep` or `discard` (the harness checks this after every scenario:
§3.3).

**Exit criterion:** the M2 scenarios pass, including every C1 lifecycle and evidence test mapped to
M2 in §3.5. CI enforces these budgets: the dashboard and `aw recap` under 100 ms for 10 lanes across
4 repositories, `aw attach` from the pool under 100 ms, and `aw new` under 150 ms until the stub
harness launches.

### M3: Guards and owner-checkout protection

- `policy`: the decision path in architecture.md. Shell commands are parsed with `mvdan.cc/sh`
  (R55). Rules: the path boundary after resolving links; owner-only commands, including inside
  substitutions, pipes and wrappers; destructive Git; container management (F5); killing
  processes; starting servers directly; reading or echoing secrets (R68, F21); the default
  publication policy, which asks (R72, its default half); team guards declared in configuration
  (R54, S9); and changes to the workspace's own configuration from a pair session (R63). Every
  error is a deny.
- `harness`: built-in adapters for the four tier-1 harnesses, Claude Code, Codex, Grok and OpenCode,
  built from recorded payload fixtures. Build Claude Code and Codex first, then Grok (ported from
  C1's adapter and tests), then OpenCode, whose adapter installs a managed plugin whose
  `tool.execute.before` handler runs `bin/aw hook opencode` and cancels the call on a deny
  (architecture.md). An unrecognized payload is denied (R56). Session-start context. Session identity and PID tracking
  (several processes for one session ID, adoption of a Codex attach by its first hook). Purpose
  capture from the first prompt. Hook configuration installed and merged with the user's existing
  settings.
- Lazy lane claiming: a write from an unattached session in the owner checkout is refused with the
  `aw attach` and `aw pair` instructions.
- `aw pair` (R60, R61) and the agent-context detection that owner-only commands need (S10).
- Git hooks dispatcher with the `reference-transaction` hook (R59), set as each repository's
  `core.hooksPath`. It runs the team's own hook of the same name afterwards.
- A first `aw setup` that installs the hooks dispatcher and the harness hook configuration, and is
  safe to rerun. M7 completes it with the remaining steps from distribution.md.
- `compose`: the generated `AGENTS.md` block from all three layers, each harness's import file,
  detection of hand edits to generated blocks (R52), and personal content kept out of the team
  repository (R53, S13).
- `aw resume` and `aw open`, using each adapter's resume flag (R77).
- Terminal multiplexers: `--mux` on `aw new`, `aw resume` and `aw pair`, with herdr and tmux built
  in and a command template for any other (the `multiplexer` key in the individual layer). Session
  liveness follows the harness process, whatever its parent. A policy rule stops lane agents from
  using a multiplexer's API (herdr's socket and CLI) to run commands outside their lane.
- The harness parity suite (R56), across all four adapters.
- `docs/safety.md`, which states the safety model (R94, first version).

**Safety invariants covered in this milestone:** guards fail closed (an invalid config, a corrupt
cache, an unreadable state database, a missing binary, an unknown payload, an unparseable command,
a computed path); the owner checkout is protected from lane agents (files, refs, shared Git
configuration, processes, data: R58); ref protection inside Git works even with `--no-verify`.

**Exit criterion:** the M3 scenarios and the parity suite pass on all four platforms against the
Git versions in the CI matrix. CI enforces the guard budget (§5.2): 8 ms p99, including the
launcher. The ref hook has the same budget (S3).

### M4: Resources and the queue

- `broker`: process and token resources (R36), seats and seat ports (S1), the FIFO queue with
  positions (R37), `--wait`, atomic multi-resource grants in a fixed resource order (R45), the owner's
  servers counted through `owner_port` (R38), owner preemption with `evict_grace` (R39), eviction
  that stops only processes and puts the lane back at the head of the queue (R40), maximum hold and
  idle expiry (R41), recovery of dead holders on the next state-changing command (R42),
  notifications through the next hook (R43), and limits on servers with idle expiry (R46).
- `procs`: process groups started with `setsid`, logs per lane, readiness probes, and process
  identity from PID plus start time, read from `/proc` on Linux and `sysctl` on macOS. A process
  whose identity is uncertain is never signalled.
- Team commands with `needs` (R44, R51), `aw serve`, `aw stop` and `aw queue` (read-only), per-lane
  hostnames and URLs (R74), and token resources with acquire and release scripts through the
  extension protocol.
- `aw finish` and `aw park` release only the lane's own leases.
- `aw doctor` warns when the capacities of all workspaces registered on the machine (S15) could
  together need more memory than the machine has, for resources that declare an approximate
  `memory`. Capacities are always fixed (§8, question 2).

**Safety invariants covered in this milestone:** eviction never touches a lane's files, branches or
data; the owner's servers always count against capacity and always win the queue; a lease that
cannot be read is treated as held.

**Exit criterion:** the M4 scenarios pass, with real processes on the host runner on both operating
systems. CI enforces these budgets: `aw serve` with a free seat under 50 ms until the server command
starts, a grant to the next queued lane under 50 ms after a release, and owner preemption under
50 ms until the evicted processes receive SIGTERM.

### M5: Lifecycle hooks and providers

- `lifecycle`: the `prepare`, `setup`, `reset` and `teardown` hooks plus each project's `install`
  with its stamp (R29, S11); the environment contract and the per-project env files (R30);
  journaled steps; the `incomplete` state, in which `aw run` refuses and the error names the retry
  command (R31); resumable teardown; parallel setup across independent projects (R32); background
  setup after `aw new` and `aw attach`, with progress shown in `aw` (R5); and waiting for exactly
  the step a command depends on (S12).
- `providers`: the interface, the built-in Postgres and Redis providers, and external providers
  through the extension protocol (R33). Data is dropped by exact recorded name and never through
  project code, nothing connects to a template (R34), and snapshots can be taken and restored by
  name (R35). The built-in providers shell out to `psql` and `redis-cli`, run on the host or with
  `docker exec` into a configured service, as C2 does. This keeps database drivers out of the
  binary and lets the acceptance suite stub them.
- `extproto`: the JSON-over-stdio extension protocol with a version number, and its error shape.
- The end-to-end suite starts here, on Linux with real Postgres and Redis containers.

**Safety invariants covered in this milestone:** providers act only on data recorded as the lane's;
a template is never connected to or dropped; a branch whose configuration would reach the owner's
databases is refused.

**Exit criterion:** the M5 scenarios pass with stubbed `psql` and `redis-cli`. The end-to-end suite
(create, snapshot, restore and drop against real Postgres and Redis) passes on Linux CI.

### M6: Devcontainer runner

- `runners/devcontainer`: one container per project, shared with VS Code; the Compose project name
  pinned to the name VS Code derives; containers started only through `devcontainer up` (F1); the
  claim step for containers left over from a moved checkout, which refuses when that other checkout
  is live and running (F2, R48); same-path mounts (S7); comparison of a running container's mounts
  and published ports with what the configuration requires, refusing a mismatch with the fix (R50,
  F3); a pinned devcontainer CLI version and the environment it needs (F4, S16); and refusal to
  recreate or stop a container while a lane or the owner uses it (R49).
- Git always runs on the host (F11). TTY detection picks `docker exec -it` or `-i`.
- A host-side keeper process for each server started in a container, so that lease liveness and
  eviction work on macOS, where container PIDs are not visible from the host (S8).
- `aw containers up|recreate|down` (owner-only).
- The end-to-end suite gains a port of C2's `smoke-docker.sh`, including moving the workspace to a
  new path and claiming its container.

**Safety invariant covered in this milestone:** the tool never starts or stops a container
unexpectedly.

**Exit criterion:** the M6 scenarios pass with stubbed `docker` and `devcontainer` on all four
platforms. The Docker end-to-end suite passes on Linux CI. The macOS checklist
(`docs/dev/macos-devcontainer-checklist.md`) has been run by hand on Docker Desktop and on OrbStack,
and its results are recorded in the PR.

### M7: Distribution and v0.1

- Signed, attested tag releases (§6), `install.sh`, `go install`, and the launcher's download path
  working against GitHub Releases.
- `bin/aw setup` from a fresh clone, with nothing but `sh`, `git`, `curl` or `wget`, and
  `sha256sum` or `shasum` (R11). It is idempotent (R12) and runs the eight steps in
  distribution.md.
- The global `aw` acts as a proxy for the workspace's pin.
- `aw doctor` complete for the v0.1 feature set.
- Harness sandboxes: where a tier-1 harness has its own sandbox (Codex and Claude Code do), `aw setup`
  configures it to allow exactly the lane, the state directory and the repositories' Git
  directories (§8, question 4; risk 7).
- Documentation: getting started, the configuration reference (generated from the schema), the
  safety model, and the supported harnesses.
- Dogfooding: at least two real workspaces move onto v0.1 release candidates without falling back
  to a candidate: the RideTracker workspace (C1's source, multi-repository, with Android and iOS
  token resources) and one container-based workspace shaped like FabWorks (C2's source), such as the
  Rails monolith whose 12 GB stack is a capacity-1 resource.
- Confirm or change the plan for archiving the candidates at the end of M8 (§9).

**Exit criterion:** `v0.1.0` is published. The `setup/fresh-clone` scenario passes on a clean
`ubuntu:24.04` container and on a macOS runner against the **published** release, verifying its
checksum and its signature. Both dogfood workspaces have been in daily use for five working days,
and any issue that would have lost work, blocked an agent wrongly or broken a budget is fixed or
recorded in this plan.

### M8: Upgrades and migration (v0.2)

`aw upgrade` with the changelog between versions, configuration migration with a diff (R10),
managed-file regeneration with edit detection and eject (R18), state migrations with a backup
(R19), lanes never stranded by an upgrade (R20), the `--drain` check from §8 (question 1), Homebrew (§6),
and a Renovate preset.

**Exit criterion:** an upgrade from v0.1.x to v0.2.0 passes in an acceptance scenario that starts
from a real v0.1 state database (committed as a fixture), with active lanes, held leases and a
hand-edited managed file. The candidates are archived (§9).

### M9: Owner workflows, delegation, roles, change sets and publication (v0.3)

`aw move-to-lane` and `aw try` (R62), delegation (R64), roles including the implementer role that
never pushes or merges (R65, F20), configurable publication policy (R72, complete), change sets
with linked merge requests and a merge order (R69), and protection of generated contracts from
hand edits (R70).

**Exit criterion:** the M9 scenarios pass, including C1's delegation scenarios.

### M10: Modules, stand-ins and plans (v0.4)

The module format and managed installation; protection of the shared stack from agents (R73);
first-party modules for a mail catcher, an object store with a bucket provider, a DNS stand-in,
pgAdmin, webhook forwarding, a browser MCP server with an isolated browser per session, the
implementer role and plans (R75, R76, F7, F15, F16, F19); and the plan mechanism (templates, the
status convention, the index, the docs checker, promotion from individual to team, and opting out:
R82, R83, R84, R66). The decisions on per-lane mail and webhook routing (§8, question 3) are made
at the start of this milestone. Build the mail catcher first: RideTracker sends email, so it can
dogfood the module at once, and webhooks are likely to follow.

**Exit criterion:** every first-party module installs, is recorded and can be ejected. The M10
scenarios pass. `aw check-docs` passes on this repository's own `plans/` and `docs/`.

### M11: Agentic onboarding (v0.5)

`aw inspect --json`, `aw doctor --projects` (R89), `aw rehearse` (R87), the workspace-design and
readiness-audit skills (R85, R86), human review of their output (R88), `aw onboard` and
`aw onboard --review`, and `aw add project` (R13).

**Exit criterion:** `aw rehearse` passes in the end-to-end suite on a fixture workspace with two
projects, and it fails with a named collision on a fixture that has a hardcoded port and a fixed
database name. The skills produce a valid `workspace.toml` for the fixture, which `aw config
validate` checks in CI.

### M12: 1.0

Documentation written for people outside the two source teams (R94, complete); declarative
adapters, so a team or module can add a hooked harness without a new release, covered by the parity
suite (architecture.md); a harness without hooks gets instructions and a documented warning (R57); the optional `aw supervise` as a user service; a performance audit of
every budget on real laptops; a security review of the guards and the launcher; removal of anything
specific to the source teams; contribution guidelines; and the public announcement.

**Exit criterion:** `v1.0.0` is published. Every requirement in §2 marked for 1.0 has at least one
passing scenario (the coverage check in §3.6). The security review's findings are fixed or
documented.

---

## 2. Traceability: R1 to R94

Milestones M0 to M7 make up v0.1, so a requirement whose milestone is M7 or earlier is complete in
v0.1. Where a requirement is split, the row names both milestones. The coverage check in §3.6 keeps this table and the scenarios' requirement tags in
agreement.

| R | Requirement (short) | Milestone | Notes |
|---|---|---|---|
| R1 | Guard decision < 8 ms p99 | M1 (stub), M3 | Enforced in CI from M1. |
| R2 | No network, engine or forge in hooks and read-only commands | M1 onward | Structural import check plus recording stubs (§5.3). |
| R3 | Command budgets | M1, M2, M4 | Each budget is enforced in the milestone that adds its command. |
| R4 | CI benchmarks fail on budgets | M0 onward | §5. |
| R5 | Slow work in the background, with progress | M2, M5 | Forge proof is opt-in in M2; setup runs in the background in M5. |
| R6 | Three configuration layers | M1 | |
| R7 | Team locks | M1 | |
| R8 | `aw config explain` | M1 | |
| R9 | Strict configuration | M1 | |
| R10 | Config schema version and migration | M1 (key), M8 | |
| R11 | `bin/aw setup` with minimal prerequisites | M7 | |
| R12 | `setup` idempotent | M7 | |
| R13 | Adding a project | M11 | |
| R14 | Read-only `aw doctor` before setup | M1, grows each milestone | |
| R15 | Exact pin with a checksum per platform | M1 (local mirror), M7 | |
| R16 | Different versions in two workspaces | M1 | |
| R17 | Lanes run the owner checkout's pin | M1 | |
| R18 | `aw upgrade`, managed files | M8 | Hashes are written from M1. |
| R19 | State schema version, migration, read-only on newer | M1 (read-only), M8 (migration) | |
| R20 | Upgrades never strand lanes | M8 | |
| R21 | Worktree per project per lane, plus data | M2, M5 (data) | |
| R22 | Names from purpose, no GUIDs | M2 | |
| R23 | Lanes belong to work | M2 | |
| R24 | Near-instant claim from the pool | M2 | Budget: 100 ms. |
| R25 | Cleanup only when clean and proven merged | M2 | Safety invariant. |
| R26 | Warm pool and retention | M2 | |
| R27 | Explicit discard, archived refs | M2 | |
| R28 | Lane guards against old branches | M2 | F9. |
| R29 | Lifecycle hooks | M5 | S11. |
| R30 | Environment contract, rerunnable, timeouts, needs | M5 | `needs` on steps uses M4's broker. |
| R31 | Journaled steps, incomplete, resumable teardown | M2 (cleanup journal), M5 | |
| R32 | Parallel setup | M5 | |
| R33 | Providers, built-in Postgres and Redis | M5 | |
| R34 | Drop by exact name, never the template | M5 | Safety invariant. S18. |
| R35 | Named snapshots | M5 | S19. |
| R36 | Process and token resources | M4 | |
| R37 | FIFO queue with positions | M4 | |
| R38 | Owner's servers count, detected by port | M4 | |
| R39 | Owner preemption | M4 | |
| R40 | Eviction stops processes only, head of queue | M4 | Safety invariant. |
| R41 | Maximum hold, idle expiry | M4 | |
| R42 | Dead holders recovered without a daemon | M4, M6 (containers) | S8. |
| R43 | Notifications through the next tool call | M4 | Delivered by the M3 hook. S6. |
| R44 | Commands declare their resources | M4 | |
| R45 | Atomic multi-resource grants | M4 | |
| R46 | Lane and server limits, idle servers expire | M2 (lanes), M4 (servers) | |
| R47 | Host and devcontainer runners, same paths | M2 (host), M6 | S7. |
| R48 | One container per project, claim step | M6 | F1, F2. |
| R49 | Never start or stop a container unexpectedly | M3 (guard), M6 | Safety invariant. |
| R50 | Ports published at creation | M6 | F3. |
| R51 | Named team commands | M4 | |
| R52 | Composed `AGENTS.md`, edit detection | M3 | |
| R53 | Personal content never dirties the team repository | M3 | S13. |
| R54 | Team guards and hooks with requirements | M3 | S9. |
| R55 | Syntax-tree guard decisions, fail closed | M3 | |
| R56 | Harness parity suite | M3 | Claude Code, Codex, Grok and OpenCode. Declarative adapters join it in M12. |
| R57 | Harnesses without hooks | M12 | The instructions themselves exist from M3. |
| R58 | Lane agents cannot disturb the owner checkout | M3 | Safety invariant. |
| R59 | `reference-transaction` hook | M3 | S3, risk 3. |
| R60 | Pair sessions, granted only by the human | M3 | |
| R61 | Pair session limits | M3 (refusals), M4 (seat 0) | |
| R62 | `move-to-lane`, `try` | M9 | |
| R63 | Human approval for workspace self-modification | M3 (pair refusal), M9 (with delegation) | |
| R64 | Delegation | M9 | |
| R65 | Roles | M9 | |
| R66 | Personal plans, notes and skills stay private | M10 | |
| R67 | Gitignored secrets, read-only links | M2 (links), M3 (guard) | F10. |
| R68 | Guards stop reading or echoing secrets | M3 | F21. |
| R69 | Change sets | M9 | |
| R70 | Generated contracts never hand-edited | M9 | |
| R71 | `gh` and `glab`, exact-head proof | M2 (proof), M9 (MR creation and linking) | |
| R72 | Publication policy | M3 (default: ask), M9 (configurable) | |
| R73 | Shared services as modules, cannot be taken down | M10 | |
| R74 | Per-lane ports, URLs and hostname | M4 | |
| R75 | Stand-in modules | M10 | |
| R76 | Team MCP servers, isolated browser per session | M10 | F19. |
| R77 | Session records, resume without IDs | M2 (records), M3 (resume) | |
| R78 | `aw finish` | M2, M4 (lease release) | |
| R79 | Park and unpark | M2 | |
| R80 | Crash and reboot recovery | M2 (sessions), M4 (leases) | |
| R81 | Status view | M2 | |
| R82 | Plan mechanism | M10 | This plan already follows the convention by hand. |
| R83 | Plans default to individual, promotion | M10 | |
| R84 | Opting out of plans | M10 | |
| R85 | Workspace-design skill | M11 | |
| R86 | Readiness-audit skill | M11 | |
| R87 | `aw rehearse` | M11 | |
| R88 | Skill output only through human review | M11 | |
| R89 | `aw doctor --projects` | M11 | |
| R90 | Linux and macOS, amd64 and arm64, real tests | M0 onward | §7. |
| R91 | Tests without Docker; separate Docker end-to-end suite | M0 (unit, acceptance), M5 (end-to-end) | |
| R92 | Black-box acceptance suite | M0 onward | §3. |
| R93 | `--json` and stable exit codes | M1 onward | Each command lands with both. |
| R94 | Safety model stated plainly | M3 (`docs/safety.md`), M12 (complete) | The README states it from v0.1. |

---

## 3. Acceptance suite

### 3.1 Goals

The acceptance suite is the main definition of correct behavior (architecture.md). It drives the
`aw` binary, its launcher and real Git as a user or agent would, with `docker`, `devcontainer`,
`gh`, `glab`, `psql`, `redis-cli` and the harnesses replaced by recording stubs. It must:

- treat `aw` only as an executable: no imports of its packages, no reading of its database schema;
- be readable and writable without knowing Go;
- run without Docker or network access (R91), on all four platforms;
- check the safety invariants automatically in every scenario, not only where a scenario remembers
  to.

### 3.2 Format and runner

Scenarios are [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) files
(`.txtar`): a short shell-like script followed by the files it needs. testscript is the format the
Go toolchain uses for its own command-line tests. It supports background processes, `wait`,
environment variables, standard input and output comparisons, and custom commands.

```
# Unmerged work keeps the lane; a merge makes it recyclable.
# req: R25 R26
# source: C1 test_generic.py::HostLaneTests::test_single_repo_lane_from_init_to_cleanup
# milestone: M2

workspace solo --single
exec aw attach 'add feature flag'
stdout '"name": "feature-flag"'
lane-commit feature-flag file.txt 'work'
exec aw sweep --json
json stdout '.lanes[0].action' keep
json stdout '.lanes[0].reason' 'not proven merged'
git-merge solo feature-flag
exec aw sweep --apply --json
json stdout '.lanes[0].action' recycle

-- workspace.toml --
[workspace]
name = "Solo"
namespace = "solo"
schema = 1
…
```

- **Layout:** `acceptance/scenarios/<area>/<name>.txtar`. The areas are listed in §3.5.
- **Header tags:** `# req:` lists requirement numbers, `# source:` names the candidate test or
  finding it came from, and `# milestone:` gives the milestone from which it must pass. Before
  that milestone the scenario is skipped and reported as pending, so scenarios can be written
  ahead of the code.
- **Runner:** `acceptance/` is its **own Go module** (`acceptance/go.mod`). A CI check fails if it
  imports anything from the main module. The runner copies the binary under test (`AW_BIN`) into a
  local release mirror for each scenario. `exec aw …` in a scenario runs the fixture workspace's
  `bin/aw` launcher, written by `aw init`, which fetches the binary from that mirror (`AW_MIRROR`)
  and verifies it. So every scenario goes through the launcher, as users do. The custom commands are
  documented in `acceptance/README.md`, so that another runner could interpret the same files.
- **Stubs** are POSIX `sh` scripts in `acceptance/stubs/`, ported from C2's
  [`tests/stubs`](../workspace-candidate-2/tests/stubs/). Each one appends its argv to `$STUB_LOG`
  and answers from files under `$STUB_STATE`. The `docker exec -w <dir> <container> <cmd>` stub
  runs `<cmd>` on the host in `<dir>`, with the env file applied. That is valid because the runner
  uses the same paths inside and outside the container (S7), and it lets runner scenarios observe
  real effects.
- **Harness stubs** (`claude`, `codex`, `grok`, `opencode`) and **multiplexer stubs** (`herdr`,
  `tmux`) are long-lived processes that record their argv, so `aw new`
  and `aw resume` can be checked, and own a PID, so session liveness can be tested. Payloads come
  from recorded fixtures in `acceptance/fixtures/payloads/<harness>/<harness-version>/`, templated
  with the scenario's paths.
- **Test tools** built by the runner from `acceptance/tools/`: `awtest-httpd` (an HTTP server that
  honors `$PORT` and logs requests) and `awtest-sigrec` (records when it receives SIGTERM, for the
  preemption budget).

### 3.3 Custom commands

| Command | Does |
|---|---|
| `workspace <name> [--single] [--repos a,b] [--forge github\|gitlab\|none]` | Builds an owner checkout at a path that contains spaces and a quote (C1 practice), with nested project repositories, bare remotes, one commit each, and the `workspace.toml` from the archive. On macOS the path sits behind the `/var` symlink, so path canonicalization is always exercised. |
| `stubs <name>…` | Puts the named recording stubs first on `PATH`. |
| `session <id> <harness> [--cwd dir]` / `end-session <id>` | Starts or kills a fake harness process with that session ID. |
| `hook <session> <event> <tool> <input-json>` | Renders the harness's payload fixture, pipes it to the launcher's hook command, and records the decoded decision. |
| `decision allow\|deny\|ask [message-regexp]` | Asserts the last hook decision, decoded by a small per-harness decoder. |
| `json <file> <path> <value>` | Asserts a value in JSON output (a small `.a.b[0]` path syntax). |
| `owner-snapshot` / `owner-unchanged` | Hashes the owner checkout's files, index, `HEAD`, refs, stash and `.git/config`. **Every scenario takes a snapshot at start and checks it at the end automatically**, unless it declares `owner-may-change` (for example, the human's own commits in a merge scenario). |
| `tree-unchanged <dir> -- <command>` | Asserts that a command writes nothing under a directory (C1's snapshot check for observational commands). |
| `lane-commit`, `git-merge`, `git-squash`, `git-cherry-pick` | Git operations on fixtures, run as the human. |
| `git-fail <args-regexp>` | Fault injection: a `git` wrapper on `PATH` that fails for matching arguments and passes everything else to the real Git. |
| `waitfor <condition> <timeout>` | Polls a file, a port or a process. |
| `pty <command>` | Runs a command under a pseudo-terminal. |
| `port` | Sets `$PORT` to a free port. |

### 3.4 Built alongside the milestones

- T2 builds the runner, the stubs and the first scenarios.
- Each milestone starts by turning its rows of §3.5 into scenario files marked with that milestone,
  so they show as pending. The milestone ends when none of them is pending.
- A pull request that changes behavior changes a scenario. A scenario may change an expected result
  only with a `# changed:` line that says why (for example, a deliberate divergence from a
  candidate).
- Unit tests sit next to the code (`go test ./...`) for logic that the black box cannot reach
  cheaply: PID reuse, rendering width, brief text truncation, path arithmetic.

### 3.5 Inventory

Each row maps one candidate test or finding to scenarios. "Changed" marks a scenario whose expected
result deliberately differs from the candidate's, because the spec chose otherwise. "Unit" means the
behavior is tested by unit tests instead. "Dropped" means the spec removed the behavior.

Scenario areas: `cli`, `config`, `state`, `launcher`, `init`, `doctor`, `whoami`, `lanes`,
`sessions`, `evidence`, `sweep`, `finish`, `recap`, `run`, `policy`, `harness`, `compose`, `broker`,
`procs`, `lifecycle`, `providers`, `runners`, `setup`, `upgrade`, `owner`, `modules`, `plans`,
`onboarding`, and `e2e` (Docker only).

#### Candidate 1: `tests/test_generic.py`

| C1 test | Scenarios | R | M |
|---|---|---|---|
| `ManifestTests.test_defaults_and_fixture` | `config/defaults`, `broker/server-start-order` | R6, R9 | M1, M4 |
| `ManifestTests.test_mistakes_are_errors_rather_than_ignored` | `config/strict-errors`: one case per C1 case, each naming file, line and fix | R9 | M1 |
| `ManifestTests.test_dependency_cycles_are_refused` | `config/server-cycle` | R9 | M1 |
| `ForgeTests.test_exact_states` | `evidence/github-states` | R71 | M2 |
| `ForgeTests.test_everything_short_of_one_exact_match_is_not_merged` | `evidence/github-not-exact`: moved head, fork, deleted repository, other base, none, two matches | R25, R71 | M2 |
| `ForgeTests.test_unavailable_api_and_pagination_limit_stay_unknown` | `evidence/forge-unknown` | R25 | M2 |
| `ForgeTests.test_adapter_choice` | `config/forge-choice`, unit (remote URL parsing) | R71 | M2 |
| `ProcessIdentityTests.*` | Unit (`internal/platform`, both operating systems), `broker/dead-holder-recovered` | R42 | M4 |
| `LauncherTests.test_mutable_refs_are_refused` | `launcher/pin-must-be-exact`: changed to an exact version plus a checksum in `.aw/lock` | R15 | M1 |
| `LauncherTests.test_broken_tool_blocks_with_exit_2` | `launcher/broken-binary-denies`, `policy/invalid-config-denies` | R55 | M1, M3 |
| `LauncherTests.test_pin_fetches_that_commit_and_runs_it` | `launcher/fetch-verify-cache`: fetch from the local mirror, refuse a wrong checksum, no second fetch | R15, R16 | M1 |
| `HostLaneTests.test_single_repo_lane_from_init_to_cleanup` | Split into `lanes/single-repo-attach`, `lifecycle/env-file-and-hooks`, `run/task-in-lane`, `broker/serve-real-http`, `whoami/seat-urls`, `sweep/unmerged-kept-merged-recycled` | R21, R25, R30, R36 | M2, M4, M5 |
| `HostLaneTests.test_guards_on_a_single_repo_lane` | `policy/single-repo-boundary` | R55, R58 | M3 |
| `HostLaneTests.test_doctor_names_each_fix` | `doctor/names-fixes` | R14 | M1, M3 |
| `ScaffoldTests.test_hook_merge_keeps_other_settings` | `compose/hook-config-merge` | R52 | M3 |
| `ScaffoldTests.test_instructions_replace_only_their_block` | `compose/agents-block-only` | R52 | M3 |

#### Candidate 1: `tests/test_lanes.py`

| C1 test | Scenarios | R | M |
|---|---|---|---|
| `test_boundary` | `policy/boundary-table`: every C1 case (own lane, owner checkout, another lane, `../`, symlink escape, `cd` tracking, own and another lane's state directory, `/tmp`, `~/.claude`, `$(pwd)`), plus file tools and globs | R55, R58 | M3 |
| `test_guard_hook_fails_closed_and_ignores_the_gate` | `policy/invalid-config-denies`, `policy/decides-while-writer-holds-lock` (C1's gate is replaced by SQLite transactions) | R1, R55 | M3 |
| `test_real_hook_and_routing` | `policy/denies-kill-container-server` (`pkill`, `docker compose down`, `npm run dev`), `harness/hook-from-project-subdir`, `policy/path-env-overrides-refused`, `cli/lane-name-validated`, `runners/mount-mismatch-refused` | R49, R55, R58 | M3, M6 |
| `test_attach_is_protected_and_idempotent` | `lanes/attach-idempotent`, `lanes/attach-leaves-owner-dirty-work`, `sessions/forged-id-refused`, `sessions/unverifiable-harness-refused` | R23, R58 | M2, M3 |
| `test_concurrent_attach_by_one_session_makes_one_lane` | `lanes/attach-concurrent`. Changed: all four callers get the same lane, instead of losers being told to retry. | R23 | M2 |
| `test_attach_exhaustion_and_process_death` | `lanes/max-lanes-names-cleanup`, `sessions/process-death-ends-attachment-keeps-lane` | R23, R46 | M2 |
| `test_attachment_records` | `sessions/stale-generation-end-ignored`, `state/corrupt-database-protects-all`, unit (PID reuse, malformed record) | R19, R23 | M2 |
| `test_resumed_process_joins_the_record` | `sessions/resumed-process-joins` | R77 | M3 |
| `test_ended_session_gets_its_lane_back` | `sessions/ended-session-returns-to-lane` | R23 | M3 |
| `test_ended_session_does_not_take_a_lane_someone_else_holds` | `sessions/stale-generation-no-return` | R23 | M3 |
| `test_read_only_git_explains_the_fix` | `state/read-only-state-dir-names-fix` | R14 | M2 |
| `test_only_session_start_revives_an_ended_record` | `sessions/only-start-revives` | R23 | M3 |
| `test_join_requires_a_verifiable_process` | `sessions/join-needs-live-process`, unit | R23 | M3 |
| `test_grok_payloads_are_normalized` | `harness/grok-payloads`, from C1's payloads, rerecorded from the current Grok CLI | R56 | M3 |
| `test_grok_attach_and_confinement` | `harness/grok-attach-and-confinement`; its cases also join the parity table | R56, R58 | M3 |
| `test_codex_attach_is_adopted_by_its_hook` | `harness/codex-attach-adopted` | R56, R77 | M3 |
| `test_codex_patches_name_their_files` | `harness/codex-apply-patch-paths`: every path in the patch is checked | R55 | M3 |
| `test_mapped_session_hooks` | `policy/attached-session-in-owner-cwd`. Changed: an unattached session in the owner checkout is refused writes with the `aw attach` and `aw pair` message (lazy claiming), where C1 allowed them. | R58 | M3 |
| `test_docs_excludes_nested_worktrees` | `plans/check-docs-skips-worktrees` | R82 | M10 |
| `test_classifier_and_observational_cli` | `sweep/preview-writes-nothing`, `sweep/untracked-file-keeps`, `sweep/active-session-keeps`, `sweep/parked-keeps` | R25, R79 | M2 |
| `test_patch_and_squash_proofs` | `evidence/patch-equivalence`, `evidence/multi-commit-squash-unproven` | R25 | M2 |
| `test_exact_mr_evidence` | `evidence/gitlab-exact-head`: head, target and source-project mismatches; API unavailable means unknown | R71 | M2 |
| `test_fake_glab_structured_responses_and_dedup` | `evidence/glab-stub`: draft, open, cached calls, duplicates are ambiguous, closed, invalid JSON is unknown | R71 | M2 |
| `test_finish_open_draft_mr_and_release_failure` | `finish/release-failure-keeps-handoff`. Changed: `finish` no longer requires a pushed branch; the handoff records push state instead. | R78 | M2, M4 |
| `test_finish_no_plan_idempotence_and_session` | `finish/idempotent`, `recap/stale-handoff-flagged`, `state/private-permissions` | R78 | M2 |
| `test_local_delivery_offline_idempotent_preserves_unmerged_work` | `finish/offline-no-network`, `sweep/finished-unmerged-kept` | R2, R25 | M2 |
| `test_local_delivery_validation_concurrency_and_release_retry` | `finish/refuses-dirty-detached-moving-head`, `finish/release-retry` | R78 | M2 |
| `test_local_handoff_reader_versions_and_resume_archive` | `sessions/invalid-handoff-needs-inspection`, `sessions/open-archives-handoff` | R77, R78 | M2, M3 |
| `test_finish_errors_and_validation` | `finish/lists-every-problem` | R78 | M2 |
| `test_finish_cli_releases_only_own_slot` | `finish/releases-only-own-leases` | R78 | M4 |
| `test_pool_age_unknown_and_caller` | `sweep/pool-size-and-retention`, `sweep/unknown-age-kept`, `sweep/caller-lane-protected` | R26 | M2 |
| `test_plain_removal_and_ignored_artifacts` | `sweep/ignored-files-block`, `sweep/generated-files-dont-block` | R25 | M2 |
| `test_change_before_apply_and_partial_failure` | `sweep/changed-since-preview-aborts`, `sweep/partial-failure-journaled-retry` | R25, R31 | M2 |
| `test_partial_worktree_retry_and_changed_head` | `sweep/retry-rechecks-proof` (uses `git-fail`) | R25, R31 | M2 |
| `test_stale_worktree_and_branch_refusal` | `sweep/foreign-worktrees-report-only` | R25 | M2 |
| `test_cli_apply_callbacks_and_idempotence` | `sweep/teardown-once-drop-by-name`, `sweep/apply-idempotent` | R31, R34 | M5 |
| `test_cli_new_reserves_reuse_with_pool_zero` | `lanes/new-protected-during-setup` | R23 | M2, M5 |
| `test_backup_locked_symlink_detached_and_unknown_session` | `sweep/unknown-means-protected`: lane marker mismatch, locked worktree, detached HEAD, unreadable session, symlinked lane directory | R25 | M2 |
| `test_gate_and_held_session_allocation` | `lanes/concurrent-sweep-and-attach` (the gate is dropped; the property is that a held lane cannot be swept) | R25 | M2 |
| `test_attachment_reservations_are_observational` | `sessions/attaching-states-protect`, `sessions/observation-writes-nothing` | R25 | M2 |
| `test_push_states_and_remote_mapping` | `recap/push-state` | R81 | M2 |

#### Candidate 1: `tests/test_tooling.py`

| C1 test | Scenarios | R | M |
|---|---|---|---|
| `test_codex_read_only_gate_names_the_writable_root_setting` | `harness/codex-sandbox-names-writable-root` | R14 | M3 |
| `test_observers_tolerate_a_never_created_gate` | `doctor/before-setup-writes-nothing`, `whoami/before-setup` | R14 | M1 |
| `test_inactive_guard_allows_only_read_only_diagnosis` | `policy/before-setup-diagnosis-only`: the C1 table, including compound commands, redirects, substitutions and newlines | R55 | M3 |
| `test_inactive_hook_wrapper_end_to_end_and_broken_import_exits_2` | `launcher/broken-binary-denies` | R55 | M3 |
| `test_fresh_checkout_bootstraps_then_attaches_without_manual_repair` | `setup/fresh-clone-then-attach`, `setup/rerun-is-noop` | R11, R12 | M7 |
| `test_delegation_lets_owner_checkout_run_only_in_scope_maintenance` | `policy/owner-only-in-substitutions` (M3); `owner/delegation-scope`, `owner/agents-cannot-grant`, `owner/expired-or-tampered-ignored`, `owner/ttl-bounds` (M9) | R64 | M3, M9 |
| `test_delegation_before_activation_allows_exact_bootstrap_commands` | `owner/delegation-before-setup` | R64 | M9 |
| `test_attach_without_project_repos_allocates_nothing` | `lanes/attach-missing-project-names-fix` | R14, R21 | M2 |
| `test_identity_is_observational_and_handles_unknown_slot` | `whoami/writes-nothing`, `whoami/unverified-seat-warns`, `whoami/no-secret-values` | R68, R93 | M1, M4 |
| `test_owner_startup_guidance_for_all_harnesses_is_observational` | `harness/session-start-guidance` | R56 | M3 |
| `test_attached_startup_preserves_routing_and_generation_checks` | `harness/session-start-in-lane`, `harness/stale-generation-denies` | R23, R56 | M3 |
| `test_identity_resolves_only_current_valid_attachment_without_writes` | `whoami/stale-attachment-is-owner`, `whoami/ambiguous-is-none` | R93 | M3 |
| `test_rt_usage_and_identity_do_not_initialize_or_acquire_writable_gate` | `cli/help-and-errors-write-nothing` (C1's legacy `rt` command is dropped) | R93 | M1 |
| `test_recovery_guidance_quotes_paths_and_never_replays_shell` | `policy/deny-message-quotes-paths` | R55 | M3 |
| `test_recap_is_observational_and_merging_does_not_complete_plan` | `recap/writes-nothing` (M2), `plans/merge-does-not-complete-plan` (M10) | R81, R82 | M2, M10 |
| `test_scope_missing_plan_stale_handoff_and_untracked_work` | `recap/lane-scope` | R81 | M2 |
| `test_no_guessing_plan_association_and_json_is_compact` | `recap/json-compact`, `recap/fits-terminal-width` | R81, R93 | M2 |
| `test_recap_view_labels_groups_and_commands` | Unit (rendering) | R81 | M2 |
| `test_brief_cuts_at_clause_boundaries` | Unit | R81 | M2 |
| `test_terminal_controls_and_no_color` | `cli/no-color-and-control-chars-stripped` | R93 | M1 |
| `test_observe_sessions_does_not_refresh_record` | `sessions/observation-writes-nothing` | R77 | M2 |
| `test_plan_status_recognizes_linked_git_file` | `plans/status-in-lane-worktree` | R82 | M10 |
| `test_default_cli_and_removed_commands_have_no_effects` | `cli/dashboard-writes-nothing` | R81 | M2 |
| `test_explicit_harness_arguments_keep_boundaries` | `lanes/new-passes-harness-args-verbatim` | R77 | M2 |
| `test_rt_slots_never_reaps` | `broker/queue-view-writes-nothing` | R2, R42 | M4 |
| `test_env_and_command_forwarding_exit_and_busy_cleanup` | `run/argv-env-exit-code`, `run/interrupt-cleans-up` | R51 | M2, M4 |
| `test_owner_container_identity_and_tty` | `runners/owner-exec-in-owner-container` | R47 | M6 |
| `test_queue_grant_release_priority_and_cancellation` | `broker/fifo-grant-release`, `broker/cancelled-wait-leaves-queue` | R37 | M4 |
| `test_setup_failure_no_wait_and_server_refusal` | `lifecycle/incomplete-refuses-run`, `broker/serve-no-wait-exit-code`, `policy/direct-server-start-denied` | R31, R37, R93 | M3, M4, M5 |
| `test_start_failure_and_stale_pid_are_protected` | `procs/start-failure-releases-seat`, `procs/uncertain-identity-never-signalled` | R42 | M4 |
| `test_unknown_config_and_symlinks_refused` | `config/unknown-table`, `finish/plan-symlink-escape-refused` | R9 | M1, M2 |
| `test_v2_guard_without_maintenance_gate` | `policy/decides-while-writer-holds-lock` | R1 | M3 |
| `test_deduplication_owner_action_and_remote_budget` | `recap/remote-deadline-unknown` (M2), `plans/effort-dedup` (M10) | R5, R81 | M2, M10 |
| `test_owner_eviction_waits_for_busy_then_notifies_and_prioritizes` | `broker/owner-preempts`. Changed: the evicted lane gets SIGTERM, then SIGKILL after `evict_grace`, instead of the owner waiting for a busy lane. | R39, R40, R43 | M4 |
| `test_report_size_and_latency_baseline` | Benchmark `dashboard-10-lanes` (§5) | R3 | M2 |
| `test_real_forwarded_stdin_arguments_exit_and_signal` | `run/stdin-and-signals` (SIGTERM gives 143, SIGINT gives 130, the child is gone) | R93 | M2 |
| `test_real_pty_selects_interactive_container_flags` | `runners/tty-flags` | R47 | M6 |
| `recorded_update` helper (tooling cutover) | Dropped: the spec leaves out C1's migration and cutover machinery | | |

#### Candidate 2: `tests/`

| C2 test | Scenarios | R | M |
|---|---|---|---|
| `t_lane_new_makes_worktree_env_and_links` | `lanes/worktree-per-project`, `lanes/shared-project-no-worktree`, `lanes/secret-links-relative`, `lifecycle/env-contract`, `providers/postgres-create`, `state/private-permissions` | R21, R30, R67 | M2, M5 |
| `t_lane_slots_and_limits` | `lanes/max-lanes-names-cleanup`, `cli/lane-name-validated`, `lanes/number-reused-after-destroy`. Changed: slots become lanes plus seats. | R46 | M2 |
| `t_lane_guard_refuses_old_branch` | `lanes/guard-refuses-old-branch` (nothing created) | R28 | M2 |
| `t_lane_existing_remote_branch_tracks` | `lanes/start-on-existing-branch`. Waits for S17. | R21 | M2 |
| `t_lane_rm_protects_unsaved_work` | `sweep/dirty-kept`, `sweep/pushed-unmerged-kept`. Changed: a pushed but unmerged lane is kept (R25); C2 allowed its removal. | R25 | M2 |
| `t_lane_rm_force_and_drop_data` | `lanes/discard-archives-then-drops`, `providers/postgres-drop-exact-names`, `providers/redis-flush-lane-db` | R27, R34 | M2, M5 |
| `t_lane_snapshot_restore` | `providers/postgres-snapshot-restore`, `providers/missing-snapshot-error` | R35 | M5 |
| `t_lane_setup_hook_and_custom_resource` | `lifecycle/setup-hook-env`, `providers/external-protocol`, `config/unknown-provider-error` | R29, R33 | M5 |
| `t_lane_var_alias` | `lifecycle/lane-env-templates` | R30 | M5 |
| `t_lane_usage` | `cli/usage` | R93 | M1 |
| `t_ws_starts_missing_container_then_execs` | `runners/devcontainer-up-then-exec` | R48 | M6 |
| `t_ws_up_only` | `runners/containers-up-runs-nothing` | R49 | M6 |
| `t_ws_lane_uses_worktree_env_and_port` | `runners/devcontainer-lane-exec`, `runners/shared-project-runs-owner-checkout` | R47, R74 | M6 |
| `t_ws_rejects_unknown` | `cli/unknown-project-or-lane`, `modules/infra-down-refused` | R73, R93 | M1, M10 |
| `t_init_claims_stale_container_and_starts_infra` | `runners/claim-stale-container`, `modules/infra-start-with-fragments` | R48, R73 | M6, M10 |
| `t_init_refuses_live_other_checkout` | `runners/claim-refuses-live-other-checkout` | R48 | M6 |
| `t_init_skips_compose_when_infra_runs` | `modules/infra-warm-start-by-service-name` | R73 | M10 |
| `t_relpath` | Unit | | M2 |
| `t_init_renders_every_placeholder` | `init/renders-workspace` | | M1 |
| `t_init_copies_core_exactly` | Dropped: the vendored core is replaced by the launcher | | |
| `t_init_refuses_existing_workspace` | `init/refuses-existing` | | M1 |
| `t_scripts_parse` | `launcher/runs-under-every-sh`: dash, bash, BusyBox ash, macOS `/bin/sh` | R11 | M1 |
| `t_update_refreshes_core_and_keeps_owned` | `upgrade/edited-managed-file-reported`. Changed: a hand edit is reported, never overwritten. | R18 | M8 |
| `t_add_project_wires_everything` | `onboarding/add-project` (M11), `runners/seat-ports-published` (M6) | R13, R50 | M6, M11 |
| `t_add_project_without_port_or_clone` | `onboarding/add-project-no-server` | R13 | M11 |
| `t_every_module_installs`, `t_module_effects` | `modules/install-each`, `modules/effects-recorded` | R73, R75 | M10 |
| `t_check_reports_problems` | `doctor/managed-file-drift` | R18 | M8 |
| `t_example_workspace_loads` | `config/examples-validate`: every example in the repository validates | R9 | M1 |
| `t_kit_usage` | `cli/usage` | R93 | M1 |
| `smoke-docker.sh` | `e2e/devcontainer-smoke`, including the moved workspace | R47, R48 | M6 |

#### Candidate 2: findings

| Finding | Scenarios | R | M |
|---|---|---|---|
| F1 Two tools, one container | `runners/compose-name-pinned`, `runners/started-only-through-devcontainer-up` | R48 | M6 |
| F2 Checkout path is identity | `runners/claim-stale-container`, `runners/claim-refuses-live-other-checkout`, `e2e/devcontainer-smoke` (moved workspace) | R48 | M6 |
| F3 Ports and mounts only at creation | `runners/seat-ports-published`, `runners/mount-mismatch-refused`, `policy/agent-cannot-recreate-container` | R49, R50 | M3, M6 |
| F4 Tooling drift | `runners/devcontainer-cli-pinned`, `doctor/devcontainer-cli-unpinned` | | M6 (S16) |
| F5 Agents created containers | `policy/container-commands-denied` (`docker run`, `docker compose up`, `devcontainer up`, `docker rm` and `stop`) | R49 | M3 |
| F6 Two workspaces on one machine | `launcher/two-workspaces-two-versions`, `doctor/port-collision-across-workspaces` | R16 | M1, M4 (S15) |
| F7 Start order must not matter | `modules/infra-warm-start-by-service-name` | R73 | M10 |
| F8 Lanes come from verification | `lanes/survive-session-end`, `finish/ready-stays-protected` | R23, R78 | M2 |
| F9 Old branches reach main databases | `lanes/guard-refuses-old-branch`, `providers/postgres-drop-exact-names` | R28, R34 | M2, M5 |
| F10 Secrets identical across lanes | `lanes/secret-links-relative`, `sweep/secret-links-dont-block`, `policy/write-through-secret-link-denied` | R67 | M2, M3 |
| F11 Worktrees inside the bind mount | `lanes/worktree-layout`, `runners/git-runs-on-host` (the docker stub never receives `git`) | R47 | M2, M6 |
| F12 Freeing slots is a procedure | The `sweep/*` scenarios. Changed: stricter, merge proof only (R25). | R25 | M2 |
| F13 Restored snapshots lag the code | `providers/restore-then-reset-hook` | R35 | M5 (S19) |
| F14 Postgres cannot copy a database in use | `providers/postgres-terminate-option`, `providers/snapshot-quiesces-lane-servers` | R35 | M5 (S19) |
| F15 Mail is shared | `modules/mail-lane-tag` | R75 | M10 (§8, question 3) |
| F16 Object storage is shared | `modules/bucket-provider-per-lane` | R33, R75 | M10 |
| F17 Third-party sandboxes are shared | `onboarding/audit-idempotency-keys`, `modules/webhook-to-registering-lane` | R75, R86 | M10, M11 |
| F18 Human-gated setup | `providers/postgres-snapshot-restore`, `lanes/secret-links-relative` | R35, R67 | M2, M5 |
| F19 The owner's browser is not a test browser | `modules/browser-per-session` | R76 | M10 |
| F20 Parallelism inside a session | `owner/implementer-role-never-pushes` | R65 | M9 |
| F21 Agents must not read secrets | `policy/secret-read-denied` (`cat .env`, `env`, `printenv`, `/proc/self/environ`), `whoami/no-secret-values` | R68 | M3 |

#### Scenarios from the spec with no candidate source

These are written from the spec directly: `policy/ref-hook-refuses-foreign-ref` and
`policy/ref-hook-ignores-no-verify` (R59), `owner/pair-*` (R60, R61), `broker/token-*`,
`broker/multi-resource-atomic`, `broker/max-hold-tail`, `broker/idle-expiry`,
`broker/owner-port-counts` (R36 to R45), `state/newer-schema-read-only` (R19),
`config/lock-refused-and-reported` (R7), `config/explain-provenance` (R8),
`lanes/pool-claim` (R24), `lanes/discard-refused-to-agents` (R27), `harness/unknown-payload-denied`,
`harness/parity` (R56), `harness/opencode-plugin-cancels-on-deny`, `harness/mux-herdr`,
`harness/mux-tmux`, `harness/mux-command-template`, `policy/mux-pane-outside-lane-denied`,
`setup/harness-sandbox-allows-lane` (M7), `doctor/machine-capacity-warning` (M4),
`compose/personal-content-untracked` (R53) and
`recap/after-reboot-interrupted` (R80).

### 3.6 Coverage check

`acceptance/coverage` reads `docs/spec/requirements.md`, the milestone column of §2 (copied into
`acceptance/requirements.toml` at T2 and kept in step with this plan) and the `# req:` tags. CI
fails when a requirement whose milestone has been reached has no passing scenario, and when a
scenario cites a requirement that does not exist.

---

## 4. Go module layout

### 4.1 Module and toolchain

- One module at the repository root: `github.com/connor981332/agent-workspace` (§9). The binary is
  `cmd/aw`. Everything else is under `internal/`, because the public interface is the command line
  and the extension protocol, not a Go API.
- The Go version is the latest stable release at T1, pinned with the `go` and `toolchain` directives
  and used by CI through `go-version-file`.
- `CGO_ENABLED=0` everywhere, so the binaries are static and cross-compile.
- Build constraints keep the code to Linux and macOS. There is no Windows code path.

### 4.2 Packages

```
cmd/aw/                     main: hot-path dispatch, then cli
internal/
  cli/                      command tree, flags, human and --json output, exit codes, picker
  app/                      flows across components: new, attach, finish, sweep, serve, setup, pair
  config/                   layers, strict schema, locks, provenance, compiled cache (writer and reader)
  state/                    SQLite store, schema versions, migrations, backup, journal, typed queries
  workspace/                finding the owner checkout and lane root, the layout, path canonicalization
  gitx/                     Git without subprocesses: .git files, HEAD, refs, packed-refs (hot-path safe)
  gitx/run/                 the Git subprocess wrapper, for operations that need Git's full logic
  lanes/                    lane records, worktrees, links, naming, pool, lane guards, change sets
  evidence/                 merge proof: ancestry, patch equivalence, forge exact head
  forge/                    gh and glab adapters, publication policy
  broker/                   resources, seats, queue, leases, preemption, expiry, recovery, notifications
  procs/                    starting, tracking and stopping process groups; readiness; keepers
  providers/                provider interface, postgres, redis, external (through extproto)
  lifecycle/                lifecycle hooks, install stamps, journaled steps, background runner
  runners/                  runner interface, host, devcontainer (identity, claim, mount and port checks)
  policy/                   the decision path and its rules
  shell/                    mvdan.cc/sh wrapper: commands, redirects, literal paths, unknowns
  harness/                  adapter interface; claude/, codex/, grok/, opencode/; payload decoding, decisions, hook config
  harness/declarative/      declarative adapters compiled into the config cache (M12)
  mux/                      terminal multiplexers: herdr, tmux, command templates
  harness/launch/           launching and resuming each harness (uses subprocesses, so not hot-path safe)
  compose/                  AGENTS.md block, import files, skills, MCP config, managed-file hashes
  sessions/                 session records, attachments, generations, handoffs, recap model
  doctor/                   checks with fixes, timings
  inspect/                  project facts for onboarding (M11), rehearsal (M11)
  upgrade/                  pins, changelog, managed-file regeneration, migration orchestration (M8)
  launcher/                 the embedded bin/aw template, fetch and verify (global proxy, setup)
  extproto/                 extension protocol client: verbs, JSON request and response, timeouts
  modules/                  module manifests and installation (M10)
  platform/                 OS specifics: process start time, flock, setsid, sysctl
  ui/                       terminal width, colors, NO_COLOR, control-character stripping
modules/                    first-party module content (data, M10)
acceptance/                 separate Go module: runner, scenarios, stubs, fixtures, tools (§3)
  bench/                    benchmark harness and budgets.toml (§5), inside the acceptance module
docs/dev/                   spikes, the macOS checklist, contributor notes
```

### 4.3 Dependency direction

```
cmd/aw ──▶ cli ──▶ app ──▶ lanes, broker, lifecycle, sessions, evidence, doctor, upgrade, compose
   │                          │        │         │
   │                          ▼        ▼         ▼
   │                  providers ──▶ runners ──▶ procs
   │                          │        │
   └─hot path─▶ harness ──▶ policy ──▶ shell
                              │
                              ▼
            config · state · workspace · gitx · extproto · platform · ui   (leaves)
```

Rules, checked by a test that runs `go list -deps`:

1. Leaves (`config`, `state`, `workspace`, `gitx`, `gitx/run`, `extproto`, `platform`, `ui`)
   import only other leaves.
2. Components do not import `app` or `cli`. Flows that touch several components live in `app`.
3. **The hot path is `cmd/aw` → `harness` → `policy` → `shell`, plus read-only use of `config`,
   `state`, `sessions`, `workspace` and `gitx`.** None of these packages except `cmd/aw` may import
   `os/exec`, `net` or `net/http`, directly or transitively. This is why `gitx/run` and
   `harness/launch` are separate packages. It enforces R2 for hooks structurally (§5.3). `cmd/aw`
   may not import them directly, but it also links the command tree (`cli`, whose flag library
   imports `net`, and later `app`, which reaches `os/exec`), so it cannot meet the transitive rule.
   Rule 4 keeps that code from running on the hot path, and T3 measures what its package
   initialization costs. Two third-party packages are exempt, by the spec owner's decision of
   2026-11-07: `modernc.org/libc` (the C library under `modernc.org/sqlite`, which imports
   `os/exec` for `system()` and `popen()`, never called by SQLite) and `github.com/google/uuid`
   (which imports `net`). The check does not descend into them; any other path to a forbidden
   package still fails, and the allowlist lives in `internal/archtest`.
4. `cmd/aw` checks for `hook` and `git-hook` as its first argument and dispatches before the command
   tree is built, so the CLI framework's setup cost never reaches the hot path.

### 4.4 Third-party libraries

| Need | Library | Why |
|---|---|---|
| Shell parser | `mvdan.cc/sh/v3` (the `syntax` package only) | Named by the spec. A real POSIX and Bash syntax tree, maintained, used by `shfmt`. |
| SQLite | `modernc.org/sqlite` | Pure Go, so the binaries stay static and cross-compile without cgo. `mattn/go-sqlite3` needs cgo. `ncruces/go-sqlite3` runs SQLite as WebAssembly, and compiling the module at startup would cost the hot path milliseconds. T3 measures the startup cost before this choice is final. |
| TOML | `github.com/pelletier/go-toml/v2` | Strict decoding (`DisallowUnknownFields`) with line and column in errors (R9), and a parser that exposes key positions for `aw config explain` (R8). The position API is in its `unstable` package, so it is wrapped in `config` and pinned. |
| CLI | `github.com/spf13/cobra` | Familiar to contributors, gives help text and shell completions for people, and supports commands added at run time (team commands). Its cost stays off the hot path through the dispatch in rule 4 above. |
| System calls | `golang.org/x/sys/unix` | flock, setsid, process start time through `sysctl` on macOS. |
| Terminal | `golang.org/x/term` | TTY detection and width. |
| Tests only | `github.com/rogpeppe/go-internal` (testscript), `github.com/creack/pty` | The acceptance format (§3.2) and the `pty` command. They are in the acceptance module only. |

There is nothing else. Fuzzy name matching, the interactive picker, JSON-path assertions and the
HTTP client for the launcher fallback use the standard library. There are no database or Docker
client libraries: providers and runners shell out, as both candidates do.

---

## 5. Latency benchmarks

### 5.1 Harness

`acceptance/bench/` is a Go program in the acceptance module. It builds a fixture workspace (10
lanes across 4 repositories, 3 lanes in the pool, 2 process resources and 1 token resource, no-op lifecycle hooks),
then runs each budgeted operation **through `bin/aw`, with the binary already cached**, as a new
process each time. It measures from just before process start to exit, which includes the launcher
(speed.md).

| Operation | Iterations | Budget (p99) | Enforced from |
|---|---|---|---|
| Hook decision, allow and deny mix, per adapter | 1000 | 8 ms | M1 (stub), M3 (real rules) |
| `reference-transaction` hook, `prepared` state | 1000 | 8 ms (S3) | M3 |
| `aw whoami`, `aw config get` | 500 | 10 ms | M1 |
| `aw` dashboard, `aw recap` (10 lanes, 4 repositories) | 200 | 100 ms | M2 |
| `aw attach` from the pool | 100 | 100 ms | M2 |
| `aw new` until the stub harness starts | 100 | 150 ms | M2 |
| `aw serve` with a free seat, until the server command starts | 100 | 50 ms | M4 |
| Grant to the next queued lane after a release | 100 | 50 ms | M4 |
| Owner preemption until SIGTERM (measured by `awtest-sigrec`) | 100 | 50 ms | M4 |

The budgets live in `acceptance/bench/budgets.toml`. A unit test parses the table in
[speed.md](../docs/spec/speed.md) and fails if the two disagree, so a budget cannot drift away from
the spec unnoticed.

Each run writes JSON (p50, p95, p99, max, and phase timings from `aw`'s debug log) and a Markdown
summary for the CI job page.

### 5.2 CI enforcement

- **Reference runners:** GitHub-hosted `ubuntu-24.04` (amd64) and `macos-15` (arm64). See S5. The
  budgets are enforced absolutely on `ubuntu-24.04` only. `macos-15` is a virtual machine whose
  process start alone costs 8 ms p99 (docs/dev/spikes.md), so there an operation fails when its p99
  grows more than 25 % over master's, with the same rerun rule. (Implemented in M1 as an A/B run:
  the job builds master's binary and measures it interleaved with the PR's on the same machine,
  comparing medians, because separate macOS runs differed by more than 2× at p99.)
- A `bench` job runs on every pull request and on every push to master, from M1 on (M0 runs the
  spike prototype only).
- An operation fails when its p99 exceeds its budget. A failing operation is measured once more in
  the same job. Two failures in a row fail CI, and both runs are reported. One noisy run on a shared
  runner cannot fail CI on its own, while a consistent regression still does.
- Results from master are kept as artifacts, and the PR summary shows the change from master's last
  run. On `ubuntu-24.04`, a p99 that grows more than 25 % is flagged in the summary
but does not fail CI.
- Twice per milestone, a maintainer runs `bench` on a real laptop (one Linux, one Apple Silicon Mac)
  and records the results in `docs/dev/benchmarks.md`, because the budgets are defined on developer
  laptops.

### 5.3 Deterministic checks

Timing on shared runners is noisy, so two checks enforce the hot-path rules without timing:

- **Imports:** the dependency rule in §4.3 (no `os/exec`, `net` or `net/http` on the hot path).
- **Recording stubs:** the bench fixture and the `policy/*` scenarios run with recording stubs for
  `git`, `docker`, `devcontainer`, `gh`, `glab`, `psql`, `curl` and `wget` first on `PATH`. Any
  call to one of them during a hook, `aw whoami` or `aw config get` fails the run (speed.md's
  hot-path rules). The dashboard, `aw recap` and `aw queue` may run `git` (dirty and ahead counts)
  but nothing else on that list, unless `--remote` is given (R2).

### 5.4 Pull requests that touch the hot path

The PR template has a "Hot path" section. A CI job lists the files changed in the hot-path packages
(§4.3, rule 3). If there are any, it requires that section to be filled in (speed.md: "A pull request
that adds work to the hot path must say why").

---

## 6. Release pipeline

| Piece | What | Lands |
|---|---|---|
| GoReleaser config | `.goreleaser.yaml`: `linux` and `darwin`, `amd64` and `arm64`, `CGO_ENABLED=0`, `-trimpath`, reproducible timestamps, version and the two schema numbers set in ldflags. The assets are raw binaries named `aw_<version>_<os>_<arch>`, so the launcher needs no `tar`. CI runs `goreleaser check` and a snapshot build on every PR. | M0 |
| Checksums | `checksums.txt` (SHA-256). `.aw/lock` holds the four checksums from it. The launcher verifies them with `sha256sum` or `shasum -a 256`. | M1 (local mirror), M2 (published) |
| Release workflow | On a `v*` tag: build, publish a GitHub Release, and run the acceptance suite against the published assets. Pre-releases `v0.0.N` mark each milestone exit from M2. The workflow uses a protected environment, so a maintainer approves each release. | M2 |
| Signing | Keyless Sigstore (cosign) signature of `checksums.txt`, through GitHub OIDC, published as a bundle. The launcher and `install.sh` verify it when `cosign` is installed. | M7 |
| Provenance | SLSA build provenance through GitHub artifact attestations (`actions/attest-build-provenance`), verifiable with `gh attestation verify`. | M7 |
| `install.sh` | Downloads the binary for the platform to `~/.local/bin/aw`, verifies the checksum, and verifies the signature when `cosign` is present. Published with each release, at a stable URL. | M7 |
| `go install` | `go install github.com/connor981332/agent-workspace/cmd/aw@vX.Y.Z`. | M7 |
| Launcher | `bin/aw`, written by `aw init` and `aw setup` from the template embedded in the binary (`internal/launcher`). Reads from `AW_MIRROR` when set. | M1 (local), M7 (GitHub Releases) |
| Global proxy | A global `aw` inside a workspace runs the workspace's pin. | M7 |
| Homebrew tap | `connor981332/homebrew-tap`, published by GoReleaser. Needs a decision on macOS quarantine for downloaded casks: Apple notarization (GoReleaser supports it), or a documented quarantine removal. | M8 |
| Renovate preset, Dependabot-compatible manifest | So that new releases arrive as merge requests that bump the pin. | M8 |
| mise, aqua, Nix | Listed in distribution.md as "later". | After 1.0 |

Each release states the framework version, the configuration schema version and the state schema
version, in the release notes and in `aw version --json`.

---

## 7. CI

The existing workflow, [`.github/workflows/candidates.yml`](../.github/workflows/candidates.yml),
stays unchanged until the candidates are archived (§9). A new workflow, `.github/workflows/go.yml`,
runs alongside it.

| Job | Runners | Runs | From |
|---|---|---|---|
| `lint` | `ubuntu-24.04` | `gofmt`, `go vet`, `staticcheck`, the dependency-direction test, the acceptance-module import check, the hot-path PR check | M0 |
| `unit` | `ubuntu-24.04`, `ubuntu-24.04-arm`, `macos-15` (arm64), `macos-15-intel` (amd64) | `go test -race ./...` | M0 |
| `acceptance` | The same four | Builds `aw` and runs the acceptance suite and the coverage check | M0 |
| `git-versions` | Linux containers: `ubuntu:22.04` (Git 2.34), `debian:bookworm` (Git 2.39), latest Git | The `policy/ref-hook-*`, `lanes/*` and `sweep/*` scenarios | M0 (spike), M3 (gating) |
| `bench` | `ubuntu-24.04`, `macos-15` | §5 | M0 (spike), M1 |
| `release-snapshot` | `ubuntu-24.04` | `goreleaser check` and a snapshot build | M0 |
| `e2e-docker` | `ubuntu-24.04` | The end-to-end suite with real Docker, Postgres, Redis and a pinned devcontainer CLI. Runs on PRs that touch `runners`, `providers` or `procs`, nightly, and before each release. | M5 |

If `ubuntu-24.04-arm` or `macos-15-intel` is not available to this repository, say so in the T1 PR
and take it to the spec owner, because R90 requires real tests on all four platforms.

macOS runners have no Docker, so the macOS devcontainer path is covered by stubbed acceptance tests
in CI and by the manual checklist at M6 and before each release (risk 4).

The Go workflow ignores changes that touch only `workspace-candidate-*` (`paths-ignore`). The
candidates workflow is not edited, so it keeps running on every pull request, as today, until the
candidates are archived.

---

## 8. Open questions

All five questions from the spec's README were settled with the spec owner on 2026-11-07. The spec
now records them under "Settled questions" in [its README](../docs/spec/README.md#settled-questions).

| # | Question | Decision | Where it lands |
|---|---|---|---|
| 1 | Does `aw upgrade` take effect at once, or offer `--drain`? | **At once**, with the backup and migration in distribution.md. Activation happens when the owner checkout's `.aw/version` changes, which is when `aw upgrade` runs there, or when a pull brings the change in. Running sessions switch at their next tool call. `--drain` is added only if a state migration cannot run safely while leases are held. | M8; a migration that needs `--drain` must say so in its PR |
| 2 | Budget-based capacity | **Not planned. Capacities are fixed.** They give the team exact control, and no shared pool could know what else runs on a developer's machine (other workspaces, a blog). Instead, `aw doctor` warns when the capacities of all workspaces registered on the machine could need more memory than it has. The spec's "later" section is replaced by the reason. | M4 (the doctor warning) |
| 3 | Per-lane isolation for mail, webhooks and sandbox accounts | **First release as in the lifecycle.md table**: a shared inbox that lanes filter by tag, a bucket per lane, webhooks forwarded to the lane that registered, shared provider accounts with snapshots, and an audit finding for idempotency keys that can repeat. Decided at the start of M10: a real inbox per lane, and webhook routing when lanes share one account. One account per lane stays out of 1.0, because a person must onboard each one (F18). RideTracker sends email and will likely take webhooks, so the mail catcher is built first and dogfooded there. | M10 start |
| 4 | Real sandboxing | **Out of scope for 1.0.** Meanwhile, `aw setup` configures each tier-1 harness's own sandbox, where it has one, to allow exactly the lane, the state directory and the Git directories. | M7 (harness sandboxes); a sandboxed runner after 1.0 |
| 5 | Which harnesses at first release | **Two tiers.** Tier 1, hooked with guards enforced, in v0.1: Claude Code, Codex, Grok and OpenCode. Tier 2: every other harness gets the composed instructions and the single entry point, and is documented as not confined (R57). Declarative adapters let a team add a hooked harness without a release (1.0). Separately, sessions open in any terminal multiplexer: herdr and tmux are built in, others through a command template. | M3 (four adapters, multiplexers), M12 (declarative adapters, R57) |

---

## 9. Decisions this plan makes

These are implementation choices the spec leaves open. Each can be revisited in review.

| Decision | Choice | Why |
|---|---|---|
| Module path | `github.com/connor981332/agent-workspace` | Matches the repository. If the repository moves to an organization, the path changes **before** `v0.1.0`, because `go install` and imports break after that. |
| Minimum Git | Provisionally 2.34 (Ubuntu 22.04), confirmed by the M0 ref-hook spike | Supports `reference-transaction` (2.28 or newer) and covers Apple Git on macOS (2.39). |
| Provider implementation | Shell out to `psql` and `redis-cli` on the configured runner | Keeps database drivers out of the binary and the dependency list, matches C2, and lets the acceptance suite stub them. |
| Lane directory numbers | Two digits (`03`), widening to three if `[lanes] max` is over 99 | The spec's layout uses `03`. |
| Plan convention for this repository | C1's Status block and plan index (R82), followed by hand until M10 ships the mechanism | Lets the plan be resumed today. |
| When to archive the candidates | At the end of M8: tag `candidates-final`, then remove `workspace-candidate-1/` and `workspace-candidate-2/` in one PR, with a README link to the tag. The candidates' CI goes with them. The inventory above cites tests by file and name, and stays valid against the tag. | By then, v0.1 has replaced both in daily use, and v0.2 has shown that upgrades work. |
| Durations in configuration | Accept seconds (`"1s"`), with no lower bound in configuration | Lets the acceptance suite test expiry and grace periods in real time, with no clock override in the binary that an agent could misuse. |

---

## 10. Risks

Ranked by likelihood times impact.

| # | Risk | Mitigation |
|---|---|---|
| 1 | **The hook latency budget (8 ms p99 including the launcher; 5 ms until the M0 spike) is missed**, especially on macOS, where process start is slower and the first run of a new binary is scanned. Every tool call of every agent pays this. | The architecture is proven in M0 before anything is built on it (T3, with a 3 ms target that leaves room for the rules). The budget is enforced from M1. The hot path is structurally limited (§4.3). The CLI framework is bypassed. If the target is missed, the fallbacks, in order: shrink package initialization on the hot path; read a small per-session snapshot file that state changes write, instead of opening SQLite (a spec change, taken to the spec owner first); keep binary size down. The cached binary is executed before the first hook, at `aw setup`, so the one-time scan never lands in a hook. **Outcome of T3:** the prototype measured 5.6 to 6.0 ms p99 on Linux, against a 3 ms target. The spec owner kept the design (launcher, SQLite, one binary) and raised the budget to 8 ms, with a 6 ms M0 target (2026-11-07). The measured savings of each fallback are in docs/dev/spikes.md, for if the budget is missed again. |
| 2 | **Harness payloads change without notice.** An adapter that fails closed blocks every tool call of that harness. | Recorded fixtures per harness version. Adapters detect the version from the payload's shape and the environment. The deny message names the fix (update `aw`, or pin the harness version). A weekly scheduled CI job diffs the hook input types published by each harness (for example the Claude Agent SDK's type definitions, Codex's hook documentation and OpenCode's plugin types) against the fixtures, and opens an issue on a change. With four built-in adapters this is the most frequent kind of maintenance, which is also why declarative adapters (M12) matter. Patch releases are reserved for adapter fixes. |
| 3 | **The `reference-transaction` hook behaves differently across Git versions**, or Git updates refs that the rule did not anticipate (fetch, stash, worktree creation, `aw`'s own operations from a lane), leading to false refusals or gaps. | The M0 spike records the behavior matrix. The allowed-ref rules are written from it (S3). The `git-versions` CI job gates M3. The guards remain the first line of protection. `aw doctor` checks the Git version and `core.hooksPath`. Fast exit for the `committed` and `aborted` states, which cannot reject. |
| 4 | **Devcontainer identity on macOS**: Docker Desktop, OrbStack and Colima differ in file sharing, path case, the `/private` symlinks and port publishing, and container PIDs are not visible from the host. GitHub's macOS runners have no Docker. | Paths are canonicalized everywhere. Same-path mounts (S7). A host-side keeper process per container server, for liveness and eviction (S8). Stubbed acceptance tests on macOS CI. A manual checklist on Docker Desktop and OrbStack at M6 and before each release. Supported engines are documented. |
| 5 | **Scope**: v0.1 is large, and the candidates keep working meanwhile, so it may slip. | Each milestone is a usable pre-release, dogfooded from M2. The deferred list is explicit. Each milestone's scenarios are written at its start, so progress is measurable. |
| 6 | **State contention**: many agents writing to SQLite at once. | WAL mode, `BEGIN IMMEDIATE` for writers, short transactions, a busy timeout, activity writes that are batched and never waited on, and a stress scenario with concurrent `attach`, `serve` and `sweep` (`lanes/attach-concurrent`, `lanes/concurrent-sweep-and-attach`). |
| 7 | **Sandboxed harnesses cannot write the state store.** Codex's default sandbox keeps `.git` read-only, and SQLite in WAL mode needs write access to its `-shm` file even to read. | `aw setup` writes the state directory into Codex's writable roots, as part of configuring each harness's own sandbox to the lane (M7). When writes fail, the error names that setting (C1 behavior, scenario `harness/codex-sandbox-names-writable-root`). `aw doctor` checks it. |
| 8 | **Merge proof wrongly proves a merge**, and a lane is destroyed. | Every C1 evidence test is carried over. Unknown means keep. Removal never forces. `discard` archives before removing. Before v0.1, a property test generates random histories and checks that proof implies the head is reachable from the base, or that a forge reported exactly that head. |
| 9 | **Process supervision without a daemon** leaves orphaned servers, or kills the wrong process. | Process identity is the PID plus its start time. A process whose identity is uncertain is never signalled. Process-group signalling only. `procs` unit tests run on both operating systems. Recovery stops orphans on the next state-changing command. |
| 10 | **The shell parser refuses ordinary commands** (false denials), which pushes people to disable guards. | A corpus of real agent commands, gathered from the maintainers' sessions with C1 and redacted, is part of the unit tests. The false-denial rate is reported in the PR summary. |
| 11 | **Intel macOS runners are retired** before 1.0, leaving `darwin/amd64` untested. | Watch GitHub's runner announcements. If the image goes, the decision about keeping `darwin/amd64` goes to the spec owner, because R90 requires real tests. |
| 12 | **Launcher compatibility**: a lane runs its own branch's copy of `bin/aw`, which may be older than the owner's. | The launcher's contract (files read, environment honored) is frozen from v0.1 and covered by a scenario that runs an old launcher against a new pin. A change to the contract requires a major version. |

---

## Spec issues

Places where the spec is ambiguous, contradicts itself or leaves out something the implementation
needs. Each row has a proposed answer. None is adopted without the spec owner's agreement, and the
"Needed by" column says by which milestone an answer must exist.

| # | Issue | Where | Proposed answer | Needed by |
|---|---|---|---|---|
| S1 | **Seats and capacity disagree.** The glossary calls a seat "one unit of a resource's capacity" with seat 0 reserved for the owner. With `capacity = 1` (the Rails example) that leaves no seat for agents, yet the example has agents holding it. | README glossary, resources.md | Capacity counts concurrent holders, including the owner when `owner_counts = true`. The seat number only chooses ports: the owner always uses seat 0 (the base ports), and agents use seats 1 to `capacity`. So the ports for seats 0 to `capacity` are published when containers are created. | M4 |
| S2 | **Who starts a granted lane's servers?** When a seat is released and the next lane is not waiting in the foreground, there is no daemon to start its servers, and the releasing command (perhaps the owner's `aw stop`) should not start them under its own session. | resources.md | A grant to a lane that is not waiting is a reservation with a claim window (`claim_window`, default 2 minutes). The agent is told on its next tool call and runs `aw serve` to start its servers. An unclaimed reservation passes to the next lane. A `--wait` caller is granted and starts its servers itself. | M4 |
| S3 | **The ref hook's allowed refs are not defined, and it has no budget.** Fetches update `refs/remotes/*`; `refs/stash` is shared by every worktree, so refusing stash rewrites means lanes cannot use `git stash`; `aw`'s own operations from a lane (finish, recycle) must pass. | owner-checkout.md, speed.md | Allowed from a lane: its own branches, worktree-private refs (`HEAD`, `ORIG_HEAD`, `FETCH_HEAD`, bisect refs), and `refs/remotes/*` from fetch. Refused: other branches, tags, `refs/aw/*`, and `refs/stash` (lanes are told to commit to a work-in-progress branch instead). `aw` authorizes its own updates with a short-lived token recorded in state. Budget: 8 ms p99, like a guard. Final rules follow the M0 spike, whose refinements are in docs/dev/spikes.md: decide at `prepared` only, allow all worktree-private pseudo-refs and `refs/bisect/*`, allow updates that leave a ref's resolved value unchanged (`pack-refs`, `gc`), and refuse `refs/notes/*`. | M3 |
| S4 | **"Plus the project's reset hooks"** in the `aw attach` and `aw new` budgets contradicts lifecycle.md, where those hooks run in the background after the command returns. | speed.md, lifecycle.md | The budgets end when the command returns or the harness launches, and they exclude every hook, which runs in the background. **Recorded in speed.md.** | M2 |
| S5 | **"Reference runners" and "developer laptop" are not defined**, so a budget cannot be enforced exactly as written. | speed.md | CI enforces the budgets on GitHub-hosted `ubuntu-24.04` (amd64), and checks `macos-15` (arm64) for regressions against master, with the rerun rule in §5.2 (amended after the M0 spike: an empty Go process costs 8 ms p99 on `macos-15`). Laptop measurements are recorded twice per milestone. **Recorded in speed.md.** | M1 |
| S6 | **The hot path must write.** **Recorded in speed.md.** Recording a session ID from its first hook call, and marking a notification delivered, are writes, but speed.md allows only activity writes. | speed.md, ux.md | Allow these two writes on the hot path, with the same rule as activity: never wait for a lock. If the write cannot be made at once, it is retried on the next call, and notifications are delivered at least once (each carries an ID, so a repeat is recognizable). | M1 |
| S7 | **Same paths inside and outside the container** (R47) conflicts with C2's template, which mounts a project at `/<directory>`. The spec takes C2's container model, but does not say how the project's committed `devcontainer.json`, which the whole team shares, gets a same-path mount and the seat ports. | requirements.md, architecture.md | The project's devcontainer uses `${localWorkspaceFolder}` as both the mount source and target, and as `workspaceFolder`. `aw` supplies the seat ports through a managed Compose fragment. `aw doctor` and the runner's mount check refuse anything else, with the fix. | M6 |
| S8 | **Lease liveness for servers inside a container**: the spec records a holder's PID and start time, but on macOS the container's processes run in a VM and are invisible from the host. | resources.md | Each container server is started by a host-side keeper (`aw` itself, detached) that runs `docker exec` and stays attached. The keeper's host PID is the lease holder. Stopping the keeper stops the server's process group in the container. If the keeper dies uncleanly, recovery stops the recorded process group through `docker exec` on the next state-changing command. | M6 |
| S9 | **Team guards as executables** (R54) would run a process on every tool call, which cannot fit the 8 ms budget. | requirements.md, architecture.md | Team guards are declarative (command patterns over the parsed syntax tree, and path rules) and run in-process. Executable team guards are allowed only for specific tool kinds, are excluded from the budget, and are documented as such. | M3 |
| S10 | **How owner-only commands recognize an agent** when the guard is not in the way (a harness without hooks, or a command run some other way). Decisions.md keeps C1's "wrapper detection" but the spec does not describe it. | owner-checkout.md, decisions.md | A call is in agent context when the harness environment markers known to an adapter are present, when `AW_SESSION` (set by `aw new`, `aw resume` and `aw pair`) is present, or when the process ancestry includes a known harness. The ancestry is checked only for owner-only commands, off the hot path. Owner-only commands refuse in agent context unless a delegation covers them. | M3 |
| S11 | **Is `install` a lifecycle hook?** Architecture.md lists `install` among the hooks; R29 does not. The state diagram also leaves out `setting-up` and `incomplete`, which the table includes. | requirements.md, architecture.md, lifecycle.md | `install` is a per-project key with a stamp, not a hook, but it is journaled like one. The diagram gains the two states. | M5 |
| S12 | **"A command that needs setup waits for exactly the step it depends on"**, but nothing declares what a command depends on. | lifecycle.md | By default, `aw run <project>` and team commands wait for the workspace's `prepare` and their project's `install` and `setup` (or `reset`). A team command can name other steps in `after = [...]`. | M5 |
| S13 | **Where personal instructions go** for each harness without dirtying the team repository (R53). | requirements.md | Each adapter declares a gitignored personal file that its harness reads (for example `CLAUDE.local.md`). `aw` composes the individual layer into it and adds it to `.git/info/exclude`. A harness without such a file gets the personal content through the session-start context. | M3 |
| S14 | **The plan state vocabulary** that R82 refers to is not in the spec. | requirements.md | Adopt C1's: `DRAFT`, `PLANNED`, `PARTIALLY IMPLEMENTED`, `FULLY IMPLEMENTED`, `FULLY VERIFIED`. This plan already uses it. | M10 |
| S15 | **Port collisions between two workspaces on one machine** (F6). Seat ports come from team configuration, so two workspaces that both use 3000 collide. | resources.md | An individual-layer `[ports] offset` added to every port of a workspace. `aw doctor` reads the other workspaces registered on the machine (`~/.local/state/agent-workspace/workspaces`) and reports overlaps. | M4 |
| S16 | **Pinning the devcontainer CLI** (F4) is not in the spec. | — | A runner key for the devcontainer CLI command and version, an environment block for it (for example `BUILDX_BAKE_ENTITLEMENTS_FS=0`), and an `aw doctor` check that it is pinned. | M6 |
| S17 | **Starting a lane on an existing branch**, such as a teammate's branch or a change set's branch, has no command. C2 supported it. | ux.md | `aw new` and `aw attach` take `--branch <name>`, which tracks the remote branch when it exists. Lane guards still run against it. **Recorded in ux.md.** | M2 |
| S18 | **Databases that project code creates**, such as Rails' parallel test databases (`shop_test_03_0`, `_1` …), are not recorded by the provider, so dropping by exact recorded name leaves them behind. C2 found and dropped them with a query. | lifecycle.md | The provider records, at `create` and after each `setup` and `reset`, every database whose name begins with one of the lane's recorded names followed by `_`. After that they are recorded names like any other. | M5 |
| S19 | **The "restore hook"** mentioned in lifecycle.md (for migrating after restoring an old snapshot, F13) is not among the defined hooks. Postgres also cannot copy a database in use (F14), and the spec does not say whether snapshotting stops the lane's servers. | lifecycle.md | Add an optional `restore` lifecycle hook, run after a provider restore. `aw snapshot` and `aw restore` stop the lane's servers first and start them again afterwards, as C1's `quiesce` did. | M5 |

---

## 12. First tasks

Each one is small enough to start at once and finishes with a reviewed PR.

### T1: Go module skeleton and CI matrix

Branch `m0/skeleton`.

1. `go.mod` at the repository root, with the module path in §9 and the toolchain pinned.
2. `cmd/aw/main.go` with the hot-path dispatch stub (§4.3, rule 4) and `internal/cli` with cobra:
   `aw version` and `aw version --json` (the framework version and both schema versions, `0` for
   now). Exit codes `0`, `1` (error) and `2` (usage) documented in `internal/cli/exitcodes.go`.
3. The `lint` and `unit` jobs from §7 in `.github/workflows/go.yml`, on all four runners, ignoring
   changes that touch only the candidate directories. `candidates.yml` is not edited.
4. The PR template with its "Hot path" section.

**Done when:** CI passes on all four runners and the candidates workflow still passes.

### T2: Acceptance harness skeleton

Branch `m0/acceptance`.

1. `acceptance/go.mod` with testscript, a runner that builds `aw` into a temporary directory and
   sets `AW_BIN`, and the import check from §3.2.
2. The `workspace` (Git topology only, paths with spaces and a quote), `stubs`, `json`,
   `owner-snapshot` and `owner-unchanged` commands, with the owner check run after every scenario
   automatically.
3. Stubs for `docker` and `devcontainer` ported from
   [C2's stubs](../workspace-candidate-2/tests/stubs/), plus a `git-fail` wrapper.
4. Scenarios `cli/version`, `cli/unknown-command-exit-code` and `cli/version-json`, and the M1 rows
   of §3.5 as pending scenario files with their headers.
5. `acceptance/requirements.toml` from §2, and the coverage check.
6. The `acceptance` job in `go.yml` on all four runners.

**Done when:** the three scenarios pass on all four runners, the pending ones are reported as
pending, and a deliberately broken `owner-unchanged` check fails a scenario.

### T3: Hot-path latency spike

Branch `spike/hot-path`. Throwaway code, never merged. The results are merged as
`docs/dev/spikes.md`.

1. A prototype launcher written to the shape in distribution.md, and a prototype `aw hook` that
   opens a SQLite database in WAL mode with `modernc.org/sqlite` and reads one row, decodes a config
   cache blob of realistic size, parses a 200-character shell command with `mvdan.cc/sh`, and prints
   a Claude Code decision.
2. The first version of `acceptance/bench/`, measuring the prototype 1000 times through the launcher on both
   reference runners and on one Linux laptop and one Apple Silicon laptop.
3. Startup cost per package (`GODEBUG=inittrace=1`) and binary size, to support or overturn the
   SQLite choice in §4.4.

**Done when:** `docs/dev/spikes.md` records p50, p95 and p99 per platform, the decision on the SQLite
driver, and the go or no-go against the 3 ms target in the M0 exit criterion.

### Next step

Start T1. T2 and T3 can run in parallel once T1's CI matrix exists. The ref-hook spike (M0) follows,
then the rest of M0.
