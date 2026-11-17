# Dogfood 0.3 plan

How aw responds to feedback gathered while dogfooding aw 0.3.0 in a large multi-repository work
workspace. There were four notes: two written by agents after sessions that went badly (an audit
in a pair session that left the session stuck in a lane, and a session recycling lanes), and two
from the owner about output. The owner and an agent triaged them on 2026-11-16 into bugs, small
improvements and questions of direction. The direction questions were settled in conversation and
recorded as the principles in [AGENTS.md](../AGENTS.md). This plan turns those decisions into work.

## Status

**State:** PARTIALLY IMPLEMENTED

**Updated:** 2026-11-16

**Branches / MRs:** #96 and #97 (AGENTS.md and this plan), #98 (Phase 1), #99 (Phase 2), #100
(Phase 3), #101 (Phase 4). Phase 5 is on `feat/dogfood-0.3-phase5` and follow-on branches.

**Done:** triage and decisions; AGENTS.md. Phases 1 to 4, each item with a scenario that fails on
master first. Unit tests, vet, the full acceptance suite (`-count=1`), the coverage check and the
latency budgets pass locally. Differences from the plan as written:
- 1.1: the dispatcher drops only `GIT_DIR` and `GIT_WORK_TREE` for a team hook in a lane, which is
  what a main worktree's hooks lack; the temporary `GIT_INDEX_FILE` Git gives a commit's hooks
  stays. Commands aw starts itself drop every repository-local variable.
- 1.5: the logs are under `.git/aw/logs/git/` (newest 20), ahead of Phase 3's step logs.
- 1.8: inspect and sweep share one matcher, which also accepts `**/name`.
- 1.9: onboard gets the same early check as audit, since they share the launch.
- 2.1: naming lanes narrows the report as well as `--apply`; `--retry` still reports every lane.
- 2.3: `aw archives --restore <name>` is a flag, as `aw sweep --apply` is, so that the human-only
  check covers it; the branch is `restored/<name>`. A bug found on the way: an unguarded agent's
  `aw sweep --retry` was not refused, since the check read the number flag as a boolean.
- 2.6: rehearse resets one lane, recycles it with `aw sweep --apply` and claims it back; with
  `[lanes] pool = 0` the lane is removed instead, which also counts as proven.
- 2.7: the reasons are printed before the question, which only a terminal is asked.
- 3.1: `ui.Out` decides live or plain from the writer (a terminal) and the harnesses' environment
  markers; it does not walk the process tree as the guards do, which would cost every command a
  process lookup. Plain output prints a step when it ends, and "still running" every 30 seconds.
- 3.2: the run log is `.git/aw/logs/runs/<command>-<time>.log`; setup uses it so far.
- 3.3: on a terminal, `aw lanes` and `aw sweep` are tables in which the purpose or reason gives way;
  off one, nothing is cut. `aw lanes` also says how to clear lanes. `aw upgrade` counts the notes.
  `aw audit` and `aw onboard` have headings, and preflight findings go to the agent in full while
  the person gets a count. `aw setup` steps through clones and installs and ends with a summary.
- 4.1: the content proof also covers histories with merge commits, which patch equivalence cannot.
  A lane made only of empty commits is, rightly, proven by it. An unproven lane's reason says how
  many files merging it would change, or that it would conflict.
- 4.2: removing a proven lane, when the pool is full, archives it too, not only recycling it.
  `[lanes] archive_retention` (default 30 days, 0 keeps them) is applied by a full
  `aw sweep --apply`, not by one aimed at lanes or a retry.
- 4.3: Git 2.34 to 2.37 is a doctor warning, since everything but the content proof works. The
  scenarios that rely on it use a new `[git-content]` condition, as CI runs sweep on Git 2.34.
- CI: the acceptance step runs with `-timeout 18m`, since macos-15-intel comes close to go test's
  default ten minutes.
- 5.1: the design note is [docs/spec/sessions.md](../docs/spec/sessions.md), reviewed by the owner
  on 2026-11-16 with four refinements: a session that started in a lane keeps its scope after
  releasing it, instead of the hook reclaiming the lane on a write (the hook stays read-only and
  fast); lazy setup starts with a command, not an edit; a harness with no session start is placed
  by its first tool call; and a full claim recycles proven lanes nobody holds.
- 5.2 to 5.5: state schema 14 (a session's home and when it released its lane; working and ready
  stored as claimed). The hook records a session it has no record of from where it is, once, and
  as holding a lane only when it can find the session's process, so that a hold always ends.
  `aw lanes` derives what it can without reading repositories. Finishing an incomplete lane makes
  it claimed, as finishing used to make it ready. Sweep still says a holding session "is attached".

**In progress:** Phase 5: 5.6 to 5.9.

**Next step:** lazy per-project setup (5.6), then capacity (5.7), the instructions (5.8) and pair
sessions for audit and onboard (5.9).

## Decisions taken

Each decision applies a principle from AGENTS.md; the number in brackets is the principle.

1. **Session identity is set when a session starts and changes only through aw's commands** [2].
   It comes from the launching command (`new`, `resume`, `pair`), or from the directory a harness
   was started in (a plain `claude` in a lane counts as choosing that lane). The working directory
   never changes it afterwards. A session that is in the wrong place is handled by the guard tiers
   (decision 5).
2. **Attach and detach behave like a stack** [2]. `attach` pushes a lane, and `detach` returns the
   session to the role it had before, so a pair session goes back to pair. A human's
   `aw pair --session` always succeeds; if the old lane has uncommitted work, it warns. Pair
   sessions hand work off with `move-to-lane` instead of attaching. `attach` starts clean, and says
   so when the owner checkout has uncommitted changes.
3. **`finish` is split into release and handoff** [2, 6]. Release is mechanical: the session no
   longer holds the lane, its leases return and its heads are recorded. It happens on `finish`, on
   session end (including process death), on `detach`, and on a human's re-pair or discard. The
   handoff is the agent's summary, and only `finish` writes it. A session that started in a lane
   and released it reclaims the lane, through lazy claiming, the next time it writes there. An
   unattached owner-checkout session that writes after a release is offered its previous lane, not
   given it automatically.
4. **Lane state is derived, not stored** [1, 6]. *In progress* means a live session holds the lane.
   *Ready for review* means nobody holds it and a handoff's heads still match. *Idle* or
   *interrupted* means nobody holds it and there is no current handoff. *Recyclable* means proven
   merged and clean. *Parked* stays stored, since it is the human's intent. `aw` and recap show
   "ended without a handoff", with `aw open <lane>` to write one.
5. **Guard tiers by who has to act** [2, 4, 8]: silent, warn, redirect (refuse and name the exact
   command), guard (ask in the harness), and hard stop (only for turning off the guards, or a call
   aw could not check). A redirect never rewrites the agent's command (R22 parity). Recovery
   commands (`aw doctor`, `git status`, `git switch`) are never refused. Asks in unattended lanes
   are fine, provided `aw` and recap show "waiting on you" prominently. `aw delegate` is not
   generalized, and overrides are not counted, until a need shows up.
6. **The route is chosen by who is present** [7]. An agent working alone gets a lane and a
   handoff; publishing follows the team's rules. A pair session edits the owner checkout directly,
   and the human reviews the diff and commits, or uses `move-to-lane` for a branch. The generated
   instructions state the precedence: the human in the conversation, then the team, then aw.
   Doctor's uncommitted-configuration warning states its consequence: new lanes won't see the
   change until it is on origin's default branch.
7. **A lane sets up a project on first use** [5, 7]: the first write, run or serve. Claiming a
   lane creates worktrees only.
8. **Prerequisites** [6]: `[prerequisites.<name>]` with `check`, `fix` and `message`, named by
   projects. They are machine-level, and the result is cached across lanes for a few minutes. aw
   checks once, before a setup that needs the prerequisite. Dependent setups wait ("waiting on
   you") and keep their slot. The human runs the fix; aw only prints it. `aw doctor` reports the
   last result without using the network (R2); `aw doctor --prerequisites` and `aw setup` run the
   checks. When the same setup step fails in several lanes with the same first error line, aw
   reports it once.
9. **Merge proof by content** [1, 6]: a fourth proof, where `git merge-tree --write-tree` of base
   and head equals the base's tree. It is checked before the forge. Every recycle not proven by
   ancestry archives the tip under `refs/aw/archive` first. Archives are pruned after a retention
   (around 30 days). Git 2.38 or later is recommended; an older Git reports the lane as "unproven,
   and why". Doctor names a remote host that `forge = "auto"` does not recognize. Every refusal
   lists each check it tried.
10. **One output layer, two renderers** [5, 6]. Building blocks: step, detail, warning, error,
    table, summary. On a terminal: live redraw, colour, and one line per finished step. Anywhere
    else, including every agent session: plain, stable text. Commands end with a summary of what
    needs the human. Step logs go to `.git/aw/logs/` (the last 50 runs, or 7 days). No TUI
    framework.
11. **Onboarding and audit** [7]. They run as pair sessions in the owner checkout by default, with
    `aw audit --lane` for one that runs on its own. The skills' endings follow decision 6 and do
    not assume a lane. The design and audit skills cover the forge and squash merges,
    prerequisites, generated globs and the cost of checkout hooks. `aw inspect` hides what is
    already declared, collapses repeated files into patterns with counts, and suggests generated
    globs typical of an ecosystem. It only suggests and never applies them.

## Phase 1: bugs

Independent of everything below. Each gets a scenario that fails on master first.

1.1 Team hooks run in a lane inherit `GIT_DIR`, `GIT_INDEX_FILE` and Git's other repository-local
    variables. A repository whose post-checkout hook clones packages then clones against the wrong
    repository. Clear `git rev-parse --local-env-vars` before running any team hook.
1.2 A recycle runs the post-checkout hook although `post_checkout = "skip"`.
1.3 A failed recycle leaves lanes half moved, with no record of which repositories moved. The
    journal also keeps failed-recycle entries after their lanes are discarded. A recycle should be
    atomic per lane, or journal each repository's step and resume it.
1.4 `aw sweep --retry` runs a full apply and recycles lanes that were not meant to be touched.
1.5 Errors are cut to "N earlier lines not shown", which hides the first failure. Write the full
    output to a log and print its path. This is an early piece of Phase 3's step logs.
1.6 `aw sweep --json` drops the per-repository merge proof when a lane is kept for another reason,
    such as a failed recycle. Compute proof whatever the reason.
1.7 When the configuration can't be read by the pinned aw, the guard refuses everything, including
    the commands that would fix it. Allow the recovery commands (decision 5).
1.8 `aw inspect` and audit report gitignored files that `generated` or other declarations already
    cover. Project names and path prefixes are inconsistent between lines, and long lists are cut
    mid-list.
1.9 Audit reports "aw doctor found nothing wrong" and then finds no free lane. Until Phase 5 makes
    audit a pair session, check for a free lane before setup.
1.10 `forge = "auto"` silently finds no forge for a host whose name doesn't contain `github` or
    `gitlab` (`internal/forge/forge.go`). Doctor names the host and the setting that fixes it.

## Phase 2: small improvements

2.1 `aw sweep --apply <lane>…` aims at specific lanes.
2.2 Every "not proven merged" message lists the checks it tried; with no forge configured, sweep
    says so when that is what keeps a lane.
2.3 `aw archives` lists archived tips; `aw archives restore <name>` restores one.
2.4 Progress output for a slow `discard`.
2.5 `aw upgrade` warns that merging an aw upgrade and a configuration change that needs it as two
    separate merges leaves the default branch broken between them.
2.6 `aw rehearse` runs a recycle (switch each worktree, run the checkout hooks) and reports each
    hook's duration.
2.7 The delegation prompt says why it is asking: which setup step failed, and what the agent will
    do with the hour.
2.8 Onboarding coverage (decision 11): the design and audit skills cover the forge and squash
    merges, generated globs and the cost of checkout hooks. Prerequisites follow in Phase 7.
2.9 `aw inspect` hides declared files, collapses repeated files into patterns with counts, and
    suggests ecosystem-typical generated globs (decision 11).

## Phase 3: output

3.1 The output layer and its two renderers in `internal/ui` (decision 10). Agent sessions are
    detected from the harness's environment markers, as the guards detect them.
3.2 Step logs under `.git/aw/logs/`, pruned to the last 50 runs or 7 days.
3.3 Convert `upgrade` (a three-line summary and `aw changelog` instead of the full notes), `audit`,
    `setup`, `sweep` and `lanes`. `aw lanes` fits the terminal width and offers cleanup commands.
    The other commands are converted when they are next touched.
3.4 Benchmarks: the layer adds nothing measurable to the budgets in `docs/spec/speed.md`.

## Phase 4: merge proof

4.1 The content proof in `internal/evidence` (decision 9), with its own proof kind.
4.2 Archive before every recycle not proven by ancestry; prune archives after the retention.
4.3 Doctor reports the Git version when it is older than 2.38.
4.4 Spec: `docs/spec/lifecycle.md` (Merge proof) and the requirement that lists the proofs.

## Phase 5: identity and lane lifecycle (the main milestone)

Decisions 1 to 4, 6, 7 and the session parts of 11, designed together and shipped together.

5.1 A design note first, in `docs/spec/`, replacing the parts of `owner-checkout.md` and
    `lifecycle.md` that change: the session record (its role stack, start identity and liveness),
    the derived states, release, and lazy per-project setup. It names the state migration and the
    requirements that change, and is reviewed before code.
5.2 Identity from the session record only; the working directory never changes identity
    (`resolveScope` in `internal/harness/hook.go`).
5.3 `aw detach`, the role stack, and `finish` as release plus handoff. Release also happens on
    session end, process death, re-pair and discard. `aw pair --session` always succeeds.
5.4 Derived lane state, and the dashboard's "waiting on you", "ended without a handoff" and
    "ready for review".
5.5 `attach` starts clean; from a pair session it redirects to `move-to-lane`, and the session
    stays paired.
5.6 Lazy per-project setup.
5.7 Capacity: a lane slot held by nobody and proven merged is recycled when a new lane is
    needed, instead of the claim failing.
5.8 The generated instructions state the precedence and the routes; doctor's
    uncommitted-configuration warning is reworded.
5.9 `aw audit` and `aw onboard` start pair sessions by default (`--lane` for one that runs on its
    own); the skills' endings are rewritten for both routes.

## Phase 6: guard tiers

Depends on Phase 5, which removes the refusals that exist only because of location.

6.1 Classify every `deny` in `internal/policy` into a tier, and record the classification in
    `docs/safety.md`.
6.2 The guard tier asks instead of denying; the hard stop keeps denying.
6.3 A read-only command from the wrong directory gets a warning, not a refusal
    (`internal/policy/rules.go`, `pathsRule`).
6.4 Every message says what it protects.
6.5 Harness parity: the harness check (R22) covers ask in each supported harness, and falls back
    to deny, saying so, where a harness can't ask.

## Phase 7: prerequisites

Depends on lazy setup (5.6).

7.1 `[prerequisites.<name>]` and `projects.<name>.prerequisites` in the schema and the reference.
7.2 Checked once before a dependent setup, cached across lanes; dependent setups wait and keep
    their slot.
7.3 `aw doctor` reports the last result offline; `aw doctor --prerequisites` and `aw setup` run the
    checks.
7.4 One report for the same failure in several lanes.
7.5 The design and audit skills propose prerequisites, including when diagnosing a failed setup.

## Verification

Local checks during a phase (unit, vet, acceptance with `-count=1`), CI once per phase, and a
release when the owner wants to use it. Phases 1 to 4 can each be released alone. Phase 5 and 6
are released together, after the owner has used them on a dev binary in a real workspace.

## Not in scope

- Counting guard overrides, and generalizing `aw delegate`: not until a need shows up.
- A guard tier for production data or credentials: aw has no concept of either, and the opt-in
  safeguards cover the rest.
- A dry run that estimates a recycle's cost: 2.6 measures hook durations instead.
- Rewriting an agent's command as a redirect: R22 parity, and the agent couldn't tell what ran.
