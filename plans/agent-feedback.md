# Agent feedback plan (aw 0.1.5)

How aw responds to three documents written on 2026-11-12 after a day of agent use in a large
multi-repo workspace on aw 0.1.5: the agent ergonomics feedback (items 1–13), the change requests
(bugs B1–B5, features F1–F6) and the owner's own feature requests (pair bypass, a lane without a
harness). Not every suggestion is taken: each section says what is kept as it is, and why.

## Status

**State:** PARTIALLY IMPLEMENTED

**Updated:** 2026-11-12

**Branches / MRs:** merged into master: #81 to #87 (Phases 1 to 4, and 5.1, 5.6, 5.7).
[#88](https://github.com/connor981332/agent-workspace/pull/88) `feedback/6-options` (5.5, F3): merged
on 2026-11-12, after every check passed. `feedback/7-devcontainer` (5.2 to 5.4, F5): local only, not pushed, branched
from master before #88 merged.

**Done:** Phases 1 to 4, 5.1, 5.5 (in #88), 5.6 as skill text, 5.7 as `on_acquire` only. Changes
from the plan as written:
- 2.6, F4: rehearsal does not guess which Compose files a host project's scripts start; a person's
  review in `accepted_findings` decides, and an accepted file's Compose project name is still
  refused beside a container command. The runtime refusal of an accepted file is not built.
- 2.2: `aw push` also records the remote name for recap and sweep's forge lookups.
- 3.1: a pair session may also delete aw's own records under `.git/aw` (flagged in #85, merged).
- 4.3: no per-lane configuration hash; `aw setup <lane>` adds what is missing and names projects the
  lane lacks. 4.5: `aw sweep` keeps a lane with open notes.
- 5.5: choices live under `[options.<name>.choices.<choice>]`; `aw rehearse --choose` is not built;
  `{lane}` and `{namespace}` are now expanded in `projects.<name>.env` too.
- 5.6: detecting alternatives in `aw inspect` is not built. 5.7: no `on_release`.

**In progress:** F5 on `feedback/7-devcontainer`. Committed there: the Compose name resolved exactly
as the devcontainer CLI 0.89.0 does (read from its source), and the F5 checks recorded in
`docs/dev/spikes.md` (checks 1 to 3 answered; check 4, VS Code's picker and Rebuild with two
configurations, needs the owner in VS Code). 5.2 and 5.3 started, with config validation, unit and
full acceptance tests passing:
- `projects.<name>.local_compose` (Mode B): aw writes its fragment (same-path mount, seat ports,
  Compose name) into the project's own gitignored local Compose file, which its committed
  devcontainer.json lists, instead of `aw.compose.yaml`. `runners.Validate(dir, local)` checks the
  right file is listed; doctor reports Mode B as OK without requiring aw's claim step.
- The `workspaceFolder = ${localWorkspaceFolder}` requirement is dropped in every mode: aw's mount
  is an extra mount, and aw runs commands at lane paths. Scenarios cover a different editor folder
  and an omitted one, plus Mode B with stubbed docker/devcontainer, seat ports and Git exclusion.
- `config.check` refuses `local_compose` on a host runner, absolute paths, `..` components and unclean
  paths. Changelog, Runners architecture and the regenerated config reference describe Mode B.
- Shared 5.2 groundwork is implemented: the fragment carries a hash of the devcontainer/Compose
  configuration, dotenv files, referenced environment values and aw's settings. A changed or missing
  label replaces only an idle service container, with volumes kept; seats, setups, attached sessions,
  unreadable state or failed/incomplete process inspection refuse replacement. Scenarios cover each
  busy/refusal path and stable reuse. Mode A remains gated on the owner's VS Code check 4.
- Onboarding offers a local Compose slot as an optional project MR, with the ownership tradeoff.
- Vet, gofmt and full unit/acceptance checks pass. Benchmarks are recorded in
  `docs/dev/benchmarks.md`: absolute budgets failed on this non-quiet run; the reduced interleaved
  comparison against master passed every regression check.

**Next step:** wait for every check on #88 to pass, merge it (approved), merge master into
`feedback/7-devcontainer` without rebasing, then push and open the F5 PR against master. Mode A
(5.4, aw generates `.devcontainer/aw/devcontainer.json`) waits for the owner's check 4 in VS Code.

## Decisions taken

1. **Pair bypasses aw's own rules, not the team's.** A pair session skips aw's built-in rules (lane
   boundary, self-modification approval, the agent refusal of owner-only commands). Team guards and
   safeguards the team enabled through `[safeguards] enable` still apply: the team chose them. This
   replaces the approval part of R61 and R63 for pair sessions.
2. **A lane may read the owner checkout; writes stay refused.** A read of a path with a lane
   counterpart is allowed with a note naming the lane's copy.
3. **Order:** guard work first (Phase 1), then cheap fixes, then the owner's requests, then
   recoveries, then devcontainer and options work.

## Findings from the code

- **B4 and item 3 (partly) are fixed on `fix/lane-attribution`:** recap shows only a lane's own work
  (`dae766a`); lanes start from origin's default branch, and the claim notes an owner checkout on
  another branch (`b592782`).
- **Item 1:** `pathsRule` (`internal/policy/rules.go`) treats any word containing `/` as a path
  (`looksLikePath`), and refuses every unknown one, read-only or not. The shell walker
  (`internal/shell/shell.go`) never resolves variables, `for` items or function calls: a function
  body is walked with an unknown directory.
- **Item 2:** `Scope.boundary` refuses any owner path from a lane, read or write; the spec says so on
  purpose (`docs/spec/owner-checkout.md`, Files row). The generated instruction "Reading needs
  nothing" (`internal/compose/compose.go`) is written for an unattached session, and reads as a
  contradiction once attached. `decideFiles` applies the boundary to the Read tool too, so the
  feedback's "Read was allowed" needs explaining (to verify).
- **Item 9:** on Git 2.56, `git branch -m aw/01 user/KEY` reports only the deletion of
  `refs/heads/aw/01` to `reference-transaction`, never the creation of the new name. The ref hook
  cannot see the rename, and the policy rule only calls it destructive, which is opt-in since
  `4bbfb18`.
- **Item 13:** `teamGuardRule` applies the first guard with paths (`firstPathGuard`) to a write whose
  target cannot be determined, with that guard's message.
- **Item 8 (to verify):** `ownerPortBusy` (`internal/broker/broker.go`) dials
  `owner_port + ports.offset`. An owner container started outside aw publishes the plain port, so a
  non-zero offset checks the wrong port.
- **B1:** `checkIsolated` (`internal/lanes/rehearsal.go`) never reads `accepted_findings`.
- **B2:** `ComposeName` (`internal/runners/devcontainer.go`) is always `<folder>_devcontainer`. That
  is also VS Code's default, so only a project with a top-level `name:` breaks.
- **B3:** `--take` reaches `modules.Refresh` only for files recorded in `.aw/managed`.
- **B5:** `aw config validate` and `aw doctor` load `ws.Owner`'s config (`internal/cli/config.go`).

## Phase 1: the guard and the reading rule

Ergonomics items 1, 2 and 13. They caused the most retries and push agents to hide commands.

1.1 **Owner-checkout reads from a lane.** `boundary` refuses only writes. A read of an owner path with
   a lane counterpart is allowed with a note ("this is the owner's copy; this lane's is `<lane>/…`"),
   through the harness's allow-with-context where it has one, and a plain allow elsewhere (the
   verdict is the same in every harness, R56). Update `owner-checkout.md`, the generated `AGENTS.md`
   block and the session-start text. `git -C <owner> status|log|merge-base` then works.

1.2 **Read-only commands with unknown paths are allowed.** With reads allowed, the boundary only
   matters for writes: `pathsRule` and an unknown `cd` refuse only when `isReadOnly` is false. When
   the opt-in secrets safeguard is on, unknown read paths still fail closed.

1.3 **Resolve what is known within the command,** in `internal/shell`:
   - assignments earlier in the same command line (`A=/x; grep … $A/docs`), until reassigned in a
     branch or loop, after which the value is unknown;
   - `for x in <literal words>`: walk the body once per item;
   - globs over a literal prefix: check the prefix (as `globPrefix` does) rather than refusing;
   - functions defined and called in the same command: walk the body at the call site, with the
     call's directory and positional arguments;
   - a relative `cd` resolves lexically even when the target does not exist yet.
   Keep it allocation-light: the hook's latency budget (`docs/spec/speed.md`) still applies, so
   benchmark before and after.

1.4 **Forge API endpoints are not paths.** The positional endpoint of `gh api` and `glab api` is
   skipped by `pathsRule`. Output files (`> x`, `--output`) are still checked.

1.5 **Item 13.** A write whose target cannot be determined gets the generic "aw cannot determine"
   refusal, never a named guard's message. A named guard applies only to a resolved path that matches
   its pattern.

1.6 **Every boundary refusal shows the accepted form,** for example "write `git -C /abs/lane/a …`;
   loops over literal words are fine".

Tests: unit cases in `internal/shell` and `internal/policy` for every command shape in the feedback
(both sessions' tables), the harness parity suite, and an acceptance scenario for a lane reading the
owner checkout. Spec: R55 and R58 wording.

## Phase 2: lane branches and cheap fixes

2.1 **Branch-rename trap (item 9).** In a lane, the policy refuses `git branch -m/-M/-c/-C` and
   creating a branch, at the first step, whatever safeguards are on: the lane branch is aw's own.
   The fix it names is `aw push --as <name>`. When the lane's `aw/<lane>` is missing, refusals say so
   and give the recovery (`git branch aw/<lane> HEAD && git switch aw/<lane>`).

2.2 **`aw push [--as <name>]`.** Pushes `aw/<lane>` under the team's branch name
   (`aw/<lane>:refs/heads/<name>`), sets the upstream, and records the remote name on the lane, so
   `recap` and `finish` show it. Respects the publication policy like any push.

2.3 **Stale owner checkout (item 3, the rest).** `aw doctor` warns when the workspace's owner
   checkout is off the default branch, behind origin as last fetched, or has uncommitted
   `workspace.toml` changes, with the fix (`git switch main && aw pull`). No fetch: doctor stays fast.

2.4 **B5.** In a lane, `aw config validate` and `aw doctor` default to the lane's `workspace.toml`.

2.5 **B3.** `aw upgrade --take <file>` writes the new version whether or not the file is recorded in
   `.aw/managed`.

2.6 **B1 and F4.** Rehearsal skips `accepted_findings` and lists them in its report, and checks only
   Compose files the config starts (servers, setup, infra). It stays strict about everything aw
   itself starts. The containers safeguard refuses starting an accepted file at runtime.

2.7 **Small items.**
   - `aw recap [lane]` takes a lane name or number as a filter (item 7).
   - `aw discard <lane>` without `--yes` prints the branches it would archive and any unpushed
     commits, then asks for `--yes` (item 7).
   - `aw inspect` lists Git repositories in the workspace root that are not projects, with
     "→ aw add project <name>" (item 6).
   - `aw add project` in a lane edits the lane's `workspace.toml` and `.gitignore`, and says the clone
     and hooks happen at `aw setup` after merge. Cloning in the owner checkout stays the human's
     (item 6).
   - The generated `AGENTS.md` block says "your lane's `scratch/`", and documents where a clone of a
     non-project repository goes and that guards do not cover it (items 2 and 11).
   - Docs: `aw finish` is right when handing off for review (item 12).

## Phase 3: the owner's requests

3.1 **Pair bypasses aw's own rules** (decision 1). In a pair session:
   - `pathsRule`, `computedRule`, `selfModRule`, `unattachedRule` and the owner-only rule allow;
   - `refuseAgent` recognizes a pair session (from `AW_SESSION` and state), so `aw upgrade`,
     `aw discard`, `aw sweep`, `aw pull` and `aw add project` work from it;
   - team guards and enabled safeguards still apply;
   - the session-start context stays, and says what the session may do.
   Update R61 and R63 in `docs/spec/requirements.md` and `owner-checkout.md`. An acceptance scenario
   runs an agentic `aw upgrade` from a pair session. This is the base for a coordinator session that
   reviews and cleans up other lanes, planned separately once this lands.

3.2 **A lane without a harness.** `aw new "<purpose>" --shell` and `aw resume <name> --shell` claim the
   lane, wait for setup, and start `$SHELL` in the lane root with the lane's environment
   (`AW_LANE_*`, the lane's env). The shell's process is recorded as the session, so liveness and
   recap work. Commands typed there are the human's, as in any terminal.

## Phase 4: recoveries and waiting

4.1 **`aw attach --wait`.** Blocks until setup finishes (reusing `awaitSetup`); on failure, prints the
   log tail and the retry command (item 5).

4.2 **`aw serve` progress.** Prints its place in line while queued ("2nd in line behind lane 03"), and
   each server's URL, port and log path as it becomes ready, and returns when every server is ready
   or one fails (item 5).

4.3 **Setup brings a lane up to the current config (item 4).** Each lane records the hash of the
   config it was set up from. `aw setup <lane>` adds what can be added at any time: secret links,
   local files, new projects' worktrees. What it cannot add is named with "recreate the lane".
   `aw lanes` and `aw recap` mark lanes set up from an older config.

4.4 **Leftover commits on a reused lane (item 10).** First find how a lane with unmerged commits was
   handed out with a new purpose (to verify). Then `aw attach` with a different purpose lists each
   project branch's commits not on its base, with their push state. `aw reset-branch [project]`
   archives the tip under `refs/aw/archive/<lane>/<time>`, as `aw discard` does, then resets to the
   base, so no harness treats it as destructive.

4.5 **`aw note "<text>"` (item 12).** Notes attached to a lane, shown by `recap` and the compaction
   hook, with a warning at `aw finish` while any are open. `aw note --done <n>` closes one.

## Phase 5: devcontainer, options and Kafka

What the owner's work workspace needs: the devcontainer runner and real Kafka without changing the
monolith.

5.1 **B2.** Reproduce in a scratch Compose project with a top-level `name:`. Then the runner adopts
   the name the project's Compose files resolve to (top-level `name:`, else `COMPOSE_PROJECT_NAME`
   from `.devcontainer/.env`, else today's default), read by aw's own Compose parser, not by running
   `docker compose config`.

5.2 **F5 groundwork, shared by both modes.** The same-path bind mount is an extra mount, so
   `workspaceFolder` stays as the project has it. aw runs the claim step itself before its own
   `devcontainer up`. The overlay labels the container with a hash of the config that built it, and
   a mismatch fails closed: an idle container is recreated, an attached one is refused with the fix.
   First, the change requests' four "To verify before building" checks.

5.3 **F5 Mode B, local override slot.** `[projects.<name>.devcontainer] local_compose = "…"`, or
   detected: aw writes its mount, ports and label there. One config, so VS Code's "Rebuild" keeps the
   mount. Built before Mode A because it is the smaller change in aw and cannot be clicked wrong.
   Onboarding offers the one-line project change as an optional MR.

5.4 **F5 Mode A, generated overlay config.** `.devcontainer/aw/devcontainer.json`, excluded through
   `.git/info/exclude`, regenerated before every start; keeps the project's `initializeCommand` and
   Compose name, drops forwarded ports aw publishes. The current committed wiring stays as a third
   mode.

5.5 **F3, team options with a choice per developer.** First version: `[options.<name>]` overlays in
   `workspace.toml`, `[choose]` in the individual file, merge order default → team → chosen
   overlays → individual, `aw config validate` validating every choice of every option,
   `aw options` listing them, and `AW_OPTION_<NAME>` for hooks and commands. Later: locks, and
   `aw rehearse --choose`.

5.6 **F1 and F2, onboarding.** Skill text first (cheap): the design skill stops and asks the human
   about each alternative setup before writing config, and records the choice; the readiness audit
   sorts findings into "aw should handle it", "project MR, optional" and "accept, with a reason".
   Then `aw inspect` reports the alternatives it can detect deterministically.

5.7 **F6, handoff hooks.** `on_acquire` and `on_release` on process resources, run with the new or old
   holder's lane environment. Document that Kafka topics stay shared across lanes.

## Kept as it is

- **Writes whose path cannot be determined fail closed.** That is the core safety rule; only reads
  get easier.
- **Lane branches stay `aw/<lane>`.** Letting a lane rename or delete branches pointing at its
  commits would complicate the ref hook, which cannot see renames anyway. Refusing the first step and
  `aw push --as` cover the need.
- **Checking only words with a leading `/`, `./` or `~`** is not adopted: relative paths with slashes
  matter for writes. Only forge API endpoints are exempt.
- **Cloning a new project stays the human's.**
- **`aw discard` keeps `--yes`;** it says what would be lost first.
- **No `aw borrow` yet.** It would add a new kind of project. Scratch clones are documented; revisit
  if this keeps coming up.
- **No `aw finish --waiting`;** a sentence in the docs is enough.
- **No separate `aw wait`;** `aw attach --wait` covers it.

## To verify

- B2 in a scratch Compose project with `name:` (5.1).
- Item 8: whether the port offset is why the owner's container did not count as a seat; then fix, or
  document what counts as the owner's copy.
- Item 2: why a Read of the owner checkout was allowed, when `decideFiles` applies the boundary.
- Item 10: how a lane with unmerged commits was handed out with a new purpose.
- F5: the change requests' four checks (5.2).

## Verification

As for the release candidate ([verification approach](release-candidate.md)): local tests during a
phase, including every acceptance scenario together; one PR per phase, running the four-platform CI
once; hot-path benchmarks recorded in `docs/dev/benchmarks.md` after Phase 1. Each change also goes
to the dev binary first (`~/.local/bin/aw-dev`) for dogfooding. Nothing is pushed without the
owner's go-ahead.
