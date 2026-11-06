# Design decisions

This candidate came from a system that ran one real product for months. These are the choices made
while generalizing it, with the alternative and the cost of each, so they can be compared with other
candidates or combined with them.

## Port the working system rather than redesign it

The value is in details learned from failures: fail-closed guards, generation checks on attachments,
journaled cleanup and the refusal to treat a gone branch as proof of merge. Most modules and their test
suites were carried over.

**Alternative:** a clean design around plugins. **Cost of this choice:** some modules are dense, and
the terminology carries history.

## Scripts instead of a database or storage abstraction

The core knows about lanes, worktrees, ports and processes. Databases and buckets are lifecycle scripts
that receive a lane number.

**Alternative:** built-in providers for Postgres, S3 and so on. Providers would make the common case
one line, but each one is a stack-specific maintenance burden, and real setups diverge quickly
(template databases, migrations, seed data, CORS rules). **Cost of this choice:** every adopter writes a
few scripts. [examples/](../examples/) shows the shape. Providers could be added later as example scripts
without touching the core.

## One strict manifest

All workspace-specific values live in `workspace.toml`, and unknown keys are errors.

**Alternative:** convention over configuration, or lenient parsing. A silently ignored guard setting is
a safety bug, so strictness wins even though it makes the tool less forgiving across versions.

## `namespace` as the only naming knob

One value derives state paths, Git metadata names, the lane marker and setting prefixes. This lets an
existing workspace keep its names, and therefore its data, and lets two workspaces share a machine.

**Cost:** container, Compose and database names stay in the manifest and scripts, because they belong
to the workspace, so the namespace does not isolate those by itself.

## Per-workspace pins resolved by a launcher

`bin/aw` is a small committed file that resolves `[tool]` to a cached checkout of an exact commit.

**Alternative:** a single global install. It is simpler, but two workspaces on one machine could not run
different versions, and an upgrade would change guards under running agents. **Cost of this choice:** the
first run needs network access, and lanes on an older base run the older `bin/aw`. The launcher copy
itself rarely changes.

## Two runners, host and Docker

The host runner is a five-minute start and suits mobile and native work. The Docker runner is the
original design: one shared container per project, with lane paths mounted at identical absolute paths
so worktree `.git` files resolve in both places.

**Alternative:** one container per lane. It isolates better but costs far more memory, which is the
constraint this project set out to manage.

**Cost:** on the host runner, agents share your user account and tool caches, so nothing but hooks
separates lanes.

## Forge adapters fail closed

A forge's "merged" permits deleting work, so adapters need exactly one same-repository change at the
lane's exact head. Anything else reports unknown or absent, and the lane is kept. Forks are excluded,
because a fork can reuse a branch name.

## Single-repository workspaces use `directory = "."`

The original assumed a workspace repository containing nested project repositories. Most repositories
are one repository, so `"."` makes the workspace repository the project. A workspace is either all
nested or a single project, because mixing the two has no clear meaning for lane bases.

## Plans are optional

Plan status, documentation checks and the pre-commit reminder ship with the tool, but nothing depends
on them except `finish --plan`, and `--no-plan` exists. Lanes are useful without adopting a planning
process.

## What was deliberately left out

- **Migration and cutover machinery.** It existed for one system's history. Upgrades here are a pin
  change.
- **The old `bin/agent` and `bin/rt` split.** Everything is `aw`. Workspaces that want the old names can
  add shims and list them in `[tool] shims`.
