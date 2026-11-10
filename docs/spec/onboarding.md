# Agentic onboarding

Writing `workspace.toml`, lifecycle hooks and provider settings by hand is the steepest part of
adopting either candidate. The framework ships agent skills that do most of that work, under one
rule: **agents propose, the tool verifies, a human approves.** The binary stays deterministic and is
the authority. Agents only author files that a person reviews.

## Skills as framework content

Skills are written once, in a harness-neutral form, and composed into each harness's skill or
command format the same way `AGENTS.md` is. They are mechanism, so the framework shipping them does
not conflict with shipping no product content (requirement R82). What they produce belongs to the
team.

They can be invoked from any supported harness, or through `aw onboard`.

`aw onboard` is the single entry point. It runs `aw setup`, reports what `aw inspect` finds, and
launches the default harness with the right skill. A failure in those deterministic steps does not end
the command: the agent starts with the failure as context, in the owner checkout as a pair session if
no lane can be created, and decides whether to fix it, skip it or ask. Nothing has to be committed: a
lane's root receives what its commit lacks, as copies that do not count as the lane's work until
they are changed. Hooks that are not aw's are left out of the onboarding lane, and the agent decides
whether the workspace keeps them, because another system's guard can refuse everything in a lane.

## Facts first: `aw inspect`

`aw inspect --json` gathers what can be found deterministically, quickly and the same way every time,
so the agent reasons from facts instead of spending time rediscovering the repositories:

- Projects, their stacks and package managers, and their lockfiles.
- Dev server commands and the ports they use, from package scripts, `Procfile`s, Compose files and
  framework configuration.
- Hardcoded `localhost:<port>` and `127.0.0.1:<port>` references.
- Environment variables each project reads.
- Database, cache, queue and object storage configuration, and whether names can vary by environment.
- Existing devcontainer and Compose definitions.
- Test, lint and build commands.
- Mobile targets (Gradle Android projects, Xcode projects).

## The workspace-design skill

Prompt, in spirit: *examine the projects in this workspace, decide where agents contend for
resources, and design the lanes, queues and hooks.*

For each project, the agent decides and explains:

- Whether lanes get a worktree or run the owner checkout with lane settings.
- Which servers exist, which are scarce, and how they group into resources with what capacity,
  preemption and time limits.
- Which data needs a provider, and which template or snapshot it starts from.
- Which lifecycle hooks are needed, and writes them.
- Which team commands exist, and what each needs.
- Which token resources exist (emulators, simulators, browsers).

It writes `workspace.toml`, the hook scripts and `docs/workspace-design.md`, which records each
decision and the reason, in the style of a decisions document.

## The readiness-audit skill

Prompt, in spirit: *what must change in each project so it works well in lanes?*

Findings, each with its fix:

- Ports that cannot be set from the environment.
- Database names that do not vary per lane.
- Fixed Redis databases, queue names, bucket names or file storage paths.
- Browser cookies scoped to `localhost`, which collide across lanes on different ports. The fix is
  the lane hostname (`AW_HOSTNAME`).
- Idempotency keys for external providers that can repeat across restored snapshots.
- Secrets read in ways that break through links, or printed in logs.
- Absolute paths that differ between host and container.

The fixes are proposed as a change set: the same branch in each affected project, linked merge
requests and a merge order. They can be handed to the implementer role.

Every finding that a program can check becomes part of `aw doctor --projects`, and the most
important ones become lane guards, so a fixed problem stays fixed and old branches that predate the
fix are refused.

## Verification: `aw rehearse`

Proposals are checked before anyone reviews them:

1. `aw config validate` and `aw doctor`.
2. `aw rehearse` creates two lanes at the same time, runs their setup, starts their servers, then
   checks that the lanes do not collide: different ports and hostnames, different database names,
   different Redis databases and buckets, no write into the owner checkout, no shared temporary
   files. It runs each project's smoke command if one is declared.
3. It tears both lanes down and confirms nothing is left: no worktrees, branches, databases,
   processes or containers.

With a lane, or from inside one, `aw rehearse` tests that lane's proposal without touching the owner:
it builds a throwaway copy of the workspace under `.worktrees/rehearsal-*`, from the lane's heads and
uncommitted changes, with its own namespace, a port offset and its own containers, and removes it
afterwards. The agent onboarding a workspace may run it for its own lane, and so needs no human for
the loop.

The skill iterates until rehearsal passes, then stops and reports.

## Review and commit

The output goes on a branch and a merge request at the team level, through the normal review for
changes to the workspace's own configuration (requirement R63). Lifecycle hooks run with the user's
privileges, so this review matters.

## Re-running

`aw onboard --review` compares the current configuration with the repositories and proposes changes
only for what is new or has drifted, such as a newly added project, a new server or a new
dependency on a database. This is the documented path for adding repositories later (requirement
R13).
