# Design decisions

The choices made while generalizing fabworkspace. Each one gives the alternative and the cost of the
choice. Where candidate 1 chose differently, that is noted, because the point of having several
candidates is to compare them.

## Keep the original's character: small POSIX shell

Fabworkspace's tooling was about 380 lines of `sh` that an agent or the owner could read in one
sitting. Candidate 2 stays in shell and stays small: the core is about 800 lines, most of them comments and usage text. Every file runs
under bash, dash, and BusyBox ash (CI covers macOS's `/bin/sh`).

**Alternative:** a program in Python or Go with real data structures, as candidate 1 is (Python).
**Cost:** shell has no local variables and weak error handling. Candidate 2's own tests caught a lane
adapter overwriting `bin/lane`'s variables, which is why adapter hooks now run in subshells. Complex
features such as queues, leases, or merge proofs would be painful to add here.

## A template plus a vendored core

`kit init` renders `template/` into files the workspace owns (`workspace.conf`, `AGENTS.md`, docs,
`infra/`) and copies `core/` (`bin/ws`, `bin/lane`, `bin/devcontainer-init`, `bin/bootstrap`,
`lib/ws/`) verbatim. `kit update` replaces only core files, so improvements reach existing workspaces
while owned files never change under the owner. `.workspace-kit` records the render values, the
modules, and the candidate commit.

**Alternative:** a pinned tool resolved by a launcher (candidate 1), or a global install. **Cost:**
updates are copies, so a local edit to a core file is overwritten. `kit check` reports such drift
first. Improvements to *owned* files, such as better wording in the template's `AGENTS.md`, don't
propagate; the owner compares by hand.

## Conventions instead of a manifest

The directory name is the Compose service, the container path, and half the Compose project name.
The infra project is `<prefix>-infra` and the network `<prefix>-dev-net`. Configuration is a sourced
`VAR=value` file, and missing settings take defaults, so an older `workspace.conf` keeps working after
an update.

**Alternative:** a strict, validated manifest (candidate 1's `workspace.toml`, where unknown keys are
errors). **Cost:** a misspelled setting is silently a new shell variable. `kit check` catches wiring
mistakes but not typos.

## Container identity is the center of the design

Fabworkspace's most hard-won idea is that VS Code, the devcontainer CLI, and agents must all resolve
to one container per project, identified by labels and checkout path, with a claim step for moved
checkouts. Candidate 2 makes this the default and only runner. Every project has a dev container, and
`bin/ws` is the one way into it.

**Alternative:** host execution as a first-class runner. **Cost:** projects that can't containerize
(mobile, native GUI) don't fit, and the first start is slow.

## Lane resources are built-in adapters, not scripts

The core knows how to give a lane its own Postgres databases and Redis database, drop them by name,
and snapshot them server-side. A workspace adds an adapter as a few `res_<name>_<hook>` functions in
`lanes.d/resources/`. `lanes.d/setup` remains for project steps like `db:prepare`.

**Alternative:** generic lifecycle scripts that receive a lane number, with no resource knowledge in
the core. Candidate 1 chose this, citing provider maintenance cost. **Cost:** stack-specific code in
the core. It earns its place because the risky parts (dropping by name, never through project code;
copying with templates; parallel test databases) are exactly what each adopter would otherwise
rewrite and get wrong. The hook interface keeps the core open to other stores.

## A lane is a fixed slot that binds everything

Slot N fixes the lane's ports (`port + N`), its Redis database, and its published host ports, which
every container publishes from creation. Slots are few (default 4) and held for the work's lifetime.

**Alternative:** a queue that hands out server slots on demand while lanes are cheap and many
(candidate 1). **Cost:** with all slots taken, a new session must wait for the owner to free one.
Nothing is reclaimed automatically.

## Same branch name in every worktree project

A lane checks out the same branch in every `worktree` project, following fabworkspace's rule for
cross-repo changes. `shared` projects (consumers that rarely change, like the slicer) run their main
checkout with the lane's settings.

**Cost:** a lane can't mix branches across repos, which a contract change in progress sometimes
wants. The workaround is git in the worktree.

## Governance by instructions, not hooks

Agent behavior is set by `AGENTS.md` (imported by `CLAUDE.md`), a single entry point (`bin/ws`), and
Claude Code permission denies for secrets. `bin/lane rm` refuses to lose work, and `bin/ws infra`
refuses to take infrastructure down. Those are the only hard stops.

**Alternative:** harness hooks that block tool calls outside the lane (candidate 1). **Cost:** nothing
stops an agent that ignores the rules, and agents without a rules file get nothing. In return, it
works the same with every harness, including ones without hooks.

## Modules for everything a lane doesn't need

Mail, object storage, DNS, pgAdmin, webhooks, the browser server, the implementer subagent, and
plans are opt-in. They are files copied into owned locations, plus appended settings, rules, and doc
rows. Infrastructure modules are Compose files in `infra/compose.d/`, merged by `COMPOSE_FILE`, so no
YAML editing is needed. Half-built modules ship with their gaps documented, at the owner's request.

**Cost:** an installed module is owned, so `kit update` doesn't improve it. Removing one is manual.

## Not carried over

- **Project content:** product docs, plans, contracts, the slicing contract, Rails-specific rules.
- **Migration notes** (MinIO to Garage, the leftover volume): history of one machine.
- **Machine-specific settings:** the Chromium path, enabled MCP servers, the internal root CA file,
  VS Code extensions.
- **GitLab and `glab`:** the forge is never used by the core.
