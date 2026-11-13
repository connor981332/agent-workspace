# Requirements

Numbered so that plans, tests and reviews can cite them. Each requirement should be checkable by a
test or a reviewer. "Wishlist n" gives the source item in the original [wishlist](../../wishlist.md);
items without one were added during design.

"Must" is required for the first public release unless the requirement says otherwise. "Should" is
expected but can follow.

## Speed

- **R1.** A guard decision for one tool call must take under 8 ms at the 99th percentile on a
  developer laptop, measured from process start to exit, including the launcher.
- **R2.** No hook, and no read-only command, may contact the network, a container engine or a forge.
- **R3.** Commands whose duration the tool controls (`aw`, `aw recap`, `aw attach`, `aw new` up to
  launching the harness, `aw serve` up to starting the server) must return within the budgets in
  [speed.md](speed.md). Time spent in the project's own commands is excluded.
- **R4.** CI must run latency benchmarks and fail on a regression against the main branch, and when
  a guard budget is exceeded. All budgets are measured on a developer laptop before each release.
- **R5.** Slow work (dependency install, setup hooks, merge proof against a forge) must run in the
  background or only when explicitly asked for, and must report progress.

## Layers and configuration (wishlist 1, 24)

- **R6.** Configuration has three layers: framework defaults, team (`workspace.toml` and installed
  modules) and individual (`~/.config/agent-workspace/<namespace>.toml`). The team can declare named
  options, the alternative setups it supports, each choice a reviewed overlay laid between the team
  and the individual layer; the individual layer picks one per option in `[choose]`, and every
  choice is validated, whether or not anyone picks it.
- **R7.** The team layer can lock any key or key pattern. A locked key cannot be overridden by the
  individual layer, and an attempt is reported, not ignored.
- **R8.** `aw config explain <key>` shows the effective value and every layer that set it.
- **R9.** Configuration is strict: unknown keys and invalid values are errors that name the file,
  line and fix.
- **R10.** Configuration carries a schema version. `aw upgrade` migrates it and shows the diff.

## Onboarding and bootstrap (wishlist 2, 24, 25)

- **R11.** A new team member runs one command after cloning, `bin/aw setup`, with nothing installed
  beforehand except `sh`, `git`, `curl` or `wget`, and `sha256sum` or `shasum`.
- **R12.** `setup` is idempotent and safe to rerun after any failure.
- **R13.** There is a documented command for adding a project repository to an existing workspace.
- **R14.** `aw doctor` works read-only before setup has completed, and every failure it reports
  names its fix.

## Framework versions (wishlist 3, 20)

- **R15.** The team pins an exact framework version with a checksum for each platform. The launcher
  verifies the checksum before running a binary.
- **R16.** Two workspaces on one machine can use different versions.
- **R17.** All lanes and hooks of a workspace run the version pinned in the owner checkout, never the
  version on a lane's branch.
- **R18.** `aw upgrade` shows the changelog, migrates configuration, regenerates managed files and
  refuses to overwrite managed files that were edited by hand.
- **R19.** Workspace state carries a schema version. A new version migrates older state forward, with
  a backup. A binary that finds state with a newer or unknown schema goes read-only and protects
  every lane.
- **R20.** Upgrading never strands an existing lane.

## Lanes (wishlist 4, 12, 14)

- **R21.** A lane has a worktree in every project configured for lanes, all on the lane's branch, plus
  its own provisioned data.
- **R22.** A lane is identified to people by a name derived from its purpose, never by a GUID.
- **R23.** Lanes belong to work. A lane survives the end, crash or restart of any session attached to
  it.
- **R24.** Claiming a lane from the warm pool must be near-instant (see [speed.md](speed.md)).
- **R25.** Cleanup removes a lane only when every repository in it is clean and its work is proven
  merged (see [lifecycle.md](lifecycle.md#merge-proof)). A gone remote branch, a handoff or a quiet
  session proves nothing.
- **R26.** Proven-merged lanes are recycled into a warm pool of configurable size. Lanes beyond the
  pool size or past the retention age are destroyed.
- **R27.** Abandoning unmerged work requires an explicit command, and the branches are archived under
  `refs/aw/archive/` before anything is removed.
- **R28.** Lane creation refuses a project branch that fails the project's lane guards, such as a
  branch whose database configuration predates per-lane names.

## Lifecycle hooks and providers (wishlist 14)

- **R29.** Teams can define lifecycle hooks for each project and for the workspace: `prepare`,
  `setup`, `reset`, `teardown`.
- **R30.** Hooks receive the documented environment contract, must be safe to rerun, and can declare
  a timeout and resources they need.
- **R31.** Hook steps are journaled. A failed step marks the lane "setup incomplete", `aw run` refuses
  in that lane, and the error names the retry command. Failed teardown can be resumed.
- **R32.** Independent projects' setup steps run in parallel.
- **R33.** Providers create, reset, snapshot, restore and drop per-lane data. The framework ships
  Postgres and Redis providers. Others are added through the extension protocol.
- **R34.** Providers drop data by exact name and never through project code. Nothing may connect to a
  provider's template.
- **R35.** Snapshots can be taken and restored by name, for example a snapshot of onboarded sandbox
  accounts.

## Resources and the queue (wishlist 5, 7, 26)

- **R36.** Teams declare resources with a capacity. A resource is either a process resource (the tool
  starts and stops its servers) or a token resource (the tool hands out a handle, such as an emulator
  serial).
- **R37.** Requests for a full resource wait in a first-in, first-out queue. Waiting agents see their
  position.
- **R38.** The owner's servers count against capacity, including servers the owner started without
  the tool, detected by their port.
- **R39.** The owner can preempt: the owner's request evicts the agent holding the seat, after a
  configurable grace period, and boots the owner's copy from the owner checkout.
- **R40.** Eviction stops only the evicted lane's processes. It never touches its files or data. The
  evicted lane rejoins the queue at the head.
- **R41.** Leases have a configurable maximum hold time while others wait, and an idle expiry.
- **R42.** A lease whose holder has died is recovered on the next state-changing command, with no
  daemon required.
- **R43.** Agents are notified of grants, evictions and expiries through their next tool call.
- **R44.** Team commands declare which resources they need. Commands that need none run immediately.
- **R45.** A request for several resources is granted all at once or not at all.
- **R46.** There are limits on concurrent lanes and on servers, and idle servers expire.

## Containers (wishlist 6)

- **R47.** Two runners: host, and devcontainer. Paths mean the same thing inside and outside the
  container.
- **R48.** VS Code, the devcontainer CLI and the tool resolve to one container per project. A
  container left over from a moved checkout is claimed or refused, never reused with an empty mount.
  An unwired Compose project gets an ignored generated configuration; its initialization and settings
  are preserved, and VS Code Rebuild keeps the selected aw configuration.
- **R49.** The tool never starts or stops a container unexpectedly. A changed devcontainer
  configuration may replace its idle container before reuse, with progress shown and volumes kept.
  Recreating or stopping a container is refused while a lane or the owner is using it; inability to
  establish that it is idle refuses replacement too.
- **R50.** Ports a lane may need are published when the container is created.

## Commands, instructions and guards (wishlist 7, 8, 9, 22)

- **R51.** Teams define named commands (test, lint, migrate and so on) that run in the right project,
  runner and lane, with the right resources held.
- **R52.** Agent instructions are composed from all three layers into one canonical `AGENTS.md`
  block. Each harness's own file imports it. Hand edits to generated blocks are detected.
  The block committed in `AGENTS.md` holds the framework and team layers. The personal layer goes to
  a file Git ignores, under R53.
- **R53.** Composed personal content never dirties the team repository. Each adapter declares a
  gitignored personal file its harness reads (`CLAUDE.local.md`), which `aw` composes and adds to
  `.git/info/exclude`. A harness without one gets it in its session-start context.
- **R54.** Teams can add guards and hooks that declare their requirements. A guard is declarative:
  command patterns over the parsed command and file patterns, decided in-process (`[guards.<name>]`,
  in the [configuration reference](../reference/config.md)). A guard's `requires` names files that
  must exist; while one is missing, everything the guard matches is refused. Executable guards are
  not part of the first release, because a process per tool call cannot fit the guard budget.
- **R55.** Shell commands are parsed into a syntax tree for guard decisions. A command whose effect
  the guard cannot determine (for example a computed path into another lane) is refused. Values the
  command itself makes known (earlier assignments, loops over literal words, functions it defines
  and calls) are followed, and a command that only reads is not refused for a path it cannot
  determine unless a rule in force depends on it.
- **R56.** Guard behavior is identical across supported harnesses. A harness parity suite verifies it
  for each adapter.
- **R57.** A harness without hooks still receives the composed instructions and a single entry
  point, and the documentation states that it is not confined.

## Owner checkout and pairing

- **R58.** Lane agents cannot write in the owner checkout, change refs outside their lane, change
  shared Git configuration, stop the owner's processes, or connect to the owner's data.
- **R59.** Ref protection is enforced inside Git by a `reference-transaction` hook as well as by the
  guards.
- **R60.** The human can grant a pair session in the owner checkout. Only the human can grant it.
- **R61.** A pair session acts with the owner's seat for resources, and aw's own rules do not apply
  to it: it may change the workspace's own configuration and run the human's commands. The team's
  guards and enabled safeguards apply to it as well.
- **R62.** Work can move from the owner checkout to a lane (`aw move-to-lane`) and a lane's head can be
  tried in the owner checkout (`aw try`).

## Self-modification, delegation and roles (wishlist 10, 23)

- **R63.** Changes to the workspace's own instructions, hooks, guards and configuration need human
  approval: a merge request from a lane, or a pair session the human started.
- **R64.** The owner can grant an expiring, scoped delegation (for example routine maintenance for one
  hour). Agents cannot grant or edit delegations.
- **R65.** The team can define roles, such as owner, implementer (never pushes or merges) and
  infrastructure agent, each with its own permissions and instructions.

## Privacy and secrets (wishlist 11, 17)

- **R66.** Personal plans, notes and skills stay out of the team repository.
- **R67.** Dev secrets live in gitignored files. Lanes link to them read-only.
- **R68.** Guards stop agents from reading or echoing secret files. Tools that need secrets read them
  directly, so agents never need to print them.

## Change sets and forges (wishlist 12, 13)

- **R69.** A change set uses the same branch name in each repository, links its merge requests to each
  other and records a merge order, producer before consumer.
- **R70.** Generated contracts are never hand-edited.
- **R71.** GitHub (`gh`) and GitLab (`glab`) are supported through adapters. A forge's report of a
  merge counts as proof only for exactly one same-repository change at the lane's exact head.
- **R72.** Guarding Git, publication, secrets, processes and containers is opt-in, through `[safeguards] enable`. With nothing enabled, `aw` regulates only its own lanes, seats and state (spec owner, 2026-11-10). With the `publication` group on, policy is configurable and the default asks before anything outward-facing. A team
  can pre-authorize narrow cases.

## Shared infrastructure and stand-ins (wishlist 15, 16, 18)

- **R73.** Shared services are optional modules. Agents cannot take the shared stack down.
- **R74.** Each lane is handed its own ports and URLs, including a per-lane hostname (such as
  `lane-03.localhost`) so browser cookies do not collide across lanes.
- **R75.** Stand-ins for external services are available as modules: mail catcher, DNS stand-in,
  object store, webhook forwarding to the right lane.
- **R76.** Teams declare MCP servers and browser tooling at the team layer. Each session gets an
  isolated browser, separate from the human's.

## Sessions and handoff (wishlist 19)

- **R77.** The tool records each session's harness, harness session ID, lane, purpose and activity.
  Resuming never requires the user to know an ID.
- **R78.** `aw finish` records exact heads, writes a handoff note, releases leases and leaves the lane
  protected.
- **R79.** Lanes can be parked and unparked.
- **R80.** After a crash or reboot, interrupted sessions are detected and shown, and stale leases are
  recovered.
- **R81.** A status view shows what needs the human, what is running, what is parked and what can be
  cleaned up.

## Plans and docs (wishlist 21)

- **R82.** The framework provides the plan mechanism (templates, a status convention a reader with no
  session memory can resume from, a plan index, a docs checker) and ships no plan content.
- **R83.** Plans and notes default to the individual level. Shared docs and plans live at the team
  level. A plan can be promoted from individual to team.
- **R84.** Teams can opt out of the plan mechanism entirely.

## Agentic onboarding

- **R85.** The framework ships a workspace-design skill that proposes projects, lanes, resources,
  providers and lifecycle hooks from facts gathered by `aw inspect`.
- **R86.** The framework ships a readiness-audit skill that finds what each project must change to
  work in lanes and proposes the changes as a change set.
- **R87.** `aw rehearse` creates two lanes at once, sets them up, starts their servers, checks that
  they do not collide and tears them down, leaving nothing behind.
- **R88.** Skill output is committed only through human review.
- **R89.** The deterministic part of the readiness audit runs as `aw doctor --projects`.

## Non-functional

- **R90.** Linux and macOS (amd64 and arm64) are supported and covered by real tests in CI. Windows is
  not supported.
- **R91.** The framework's own tests run without Docker, against stubbed container tooling and real
  Git. A separate end-to-end suite uses Docker.
- **R92.** A black-box acceptance suite drives the `aw` binary. It is independent of the
  implementation language.
- **R93.** Every command offers `--json` output and stable exit codes for scripts and agents.
- **R94.** The documentation states the safety model plainly: guards are for cooperative agents, not a
  sandbox.
