# Design decisions

Each decision gives the choice, the alternative and the cost, in the same form as the candidates'
own decisions documents. "C1" is [`workspace-candidate-1`](../../workspace-candidate-1/) (Python,
from the RideTracker workspace). "C2" is [`workspace-candidate-2`](../../workspace-candidate-2/)
(shell, from the FabWorks workspace).

## A new Go codebase, with both candidates as the specification

Neither candidate is refactored into the release. C1 contributes its safety invariants and the
scenarios in its tests. C2 contributes container identity, providers, modules and its
[findings](../../workspace-candidate-2/docs/findings.md).

**Alternative:** refactor C1, which is closest in scope. **Cost of this choice:** a rewrite takes
longer to reach parity, and proven code is left behind. But the missing pieces (layered
configuration, state versioning, multi-repository change sets, providers, modules, the devcontainer
runner, a real shell parser) touch nearly every C1 module, and the language changes anyway. The
behavior that matters is kept through the acceptance suite, not the code.

## Go

- `mvdan.cc/sh` gives the guards a real shell syntax tree. C1 uses regular expressions, and its own
  safety notes admit that computed paths get past them.
- Startup in milliseconds, which the hot-path budget needs. Python spends tens of milliseconds on
  interpreter startup and imports.
- A static binary with no runtime to install. C1 needs Python 3.11 or newer, which macOS does not
  provide by default.
- Pure-Go SQLite, strict TOML decoding and the container ecosystem are available.
- Approachable for contributors and for agents.

**Alternatives:** Rust (no mature shell parser, a higher bar for contributors), Python (startup,
runtime install), shell (C2's own decisions say queues, leases and merge proof would be painful).

## The tool is never the bottleneck

Latency budgets are requirements, enforced in CI ([speed.md](speed.md)). This shapes other decisions:
no daemon on the hot path, a compiled configuration cache, a launcher made of shell builtins, the
warm pool and lazy lane claiming.

**Cost:** some designs that would be simpler are ruled out, such as rereading TOML or asking Git
on every call, and the benchmark suite has to be maintained.

## Strict, layered TOML configuration (from C1, extended)

Unknown keys are errors, because a silently ignored guard setting is a safety bug. Three layers with
team locks and provenance.

**Alternative:** C2's sourced shell variables with defaults, which tolerate old files. **Cost:**
upgrades must migrate configuration, which `aw upgrade` does.

## Providers for data, hooks for project steps (combines C1 and C2)

C1 left all per-lane data to team scripts, to keep stack knowledge out of the core. C2 built Postgres
and Redis adapters into the core, because dropping by name and cloning from templates are exactly
what every team would otherwise get wrong. The release does both: a provider interface with
built-in Postgres and Redis providers that teams can replace, and lifecycle hooks for project steps
such as `db:prepare`.

**Cost:** the framework maintains two providers. Others come through the extension protocol.

## Many cheap lanes, scarce resources leased from a queue (from C1)

C2 bound everything to a few fixed slots: ports, Redis databases, published ports. A new session had
to wait for the owner to free a slot. The release follows C1: lanes are cheap, and only scarce
things are rationed, through a fair queue with owner preemption ([resources.md](resources.md)).

**Cost:** a queue, leases and recovery are more machinery than fixed slots.

## Ports follow seats, not lanes

A server's port is a function of the seat it holds, so every port a lane might use is known in
advance and published when a container is created (C2, finding 3). Lanes also get a hostname each,
so cookies do not collide even when a port is reused.

**Cost:** a lane's URL changes if it gets a different seat. `aw serve` and `aw whoami` print the
current URL.

## No required daemon

State changes and recovery happen lazily inside each command, under a transaction. An optional
supervisor makes expiry and notifications prompt.

**Alternative:** a broker daemon that owns the queue. **Cost of this choice:** expiry happens at the
next command rather than on time, unless the supervisor runs. The benefit is no install step and no
failure mode that guards would have to fail closed on.

## SQLite for state

C1 used JSON files and `fcntl` locks, C2 used env files. Leases, queues and journals need
transactions and crash safety, which SQLite in WAL mode provides, with readers that never block.

**Cost:** state is not readable with a text editor. `aw state dump` covers debugging.

## Guards and instructions, both (combines C1 and C2)

C1 enforced lanes with harness hooks. C2 used instructions only, so that every harness behaves the
same. The release enforces with hooks where a harness supports them, composes instructions for every
harness, and verifies with a parity suite that hooked harnesses decide identically. A harness without
hooks gets instructions and a documented warning that it is not confined.

## Ref protection inside Git

The shell parser cannot see every way a command changes refs. A `reference-transaction` hook refuses
ref updates from a lane outside its own branches, inside Git itself.

**Cost:** `aw` must own `core.hooksPath` and dispatch to the team's hooks.

## Container identity from C2, mount checks from C1

The devcontainer runner uses C2's model: one container per project, shared with VS Code, started
through `devcontainer up`, with a claim step for moved checkouts. It adds C1's check that a running
container's mounts and ports match what the configuration needs.

**Cost:** project worktrees must live inside each project directory, so the lane root links to them.
C1 instead mounted only the lanes and `.git` directories, which kept agents in containers from
reaching the owner's working tree. With C2's layout, the owner's working tree is visible inside the
container, and its protection there is the guards, not the mount.

## Lanes belong to work and are recycled through a warm pool

From C2's finding 8: lanes must outlive sessions because verification happens later. From C1:
merged lanes are reused. Lanes are numbered and stable on disk, and named after their current work.

**Alternative:** create a lane per session and destroy it at the end. **Cost of this choice:** lanes
exist when no session is running, and the dashboard must show them clearly.

## Lazy lane claiming

A session started directly in the workspace claims a lane on its first write, not at start.

**Alternative:** claim at session start. **Cost of this choice:** the first edit in each session hits a
guard message and the agent must run `aw attach`.

## One active framework version per workspace

Lanes run the owner checkout's pin. C1 let each lane run the `bin/aw` on its own branch, which could
leave old and new versions acting on shared state.

**Cost:** a lane's branch cannot test a framework upgrade on its own. Maintainers working on `aw` use
`AW_TOOL_PATH`.

## Pinned per workspace, fetched by a committed launcher (from C1)

C2 copied its core into each workspace with `kit update`, which overwrote local edits. C1 resolved an
exact commit through a launcher. The release keeps the launcher but pins a signed release binary
instead of a source commit, and manages generated files by hash.

## Agents propose, the tool verifies, a human approves

Onboarding skills write configuration and hooks. `aw rehearse` checks them. A merge request gets
them reviewed. Agents never change live workspace configuration directly.

## Kept from the candidates

From C1: merge proof (ancestry, patch equivalence, forge exact head, no forks), fail-closed guards,
attachment generations, journaled teardown, owner-only commands and wrapper detection, delegation,
the plans mechanism and docs checker, the doctor that names fixes, `install_stamp`, `generated`
files that do not block removal.

From C2: container identity and the claim step, providers with drop by name and template snapshots,
lane guards against old branches, secret links that are relative and excluded from Git, the shared
infrastructure project and warm start check, modules, the implementer role, the isolated browser per
session, every finding in `findings.md`.

## Left out

- C1's migration and cutover machinery for its original system.
- C2's `kit` copy-and-render model, replaced by managed files.
- Windows support.
