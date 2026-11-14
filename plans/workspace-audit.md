# Workspace audit plan

A conversation with an agent about a workspace configuration that already exists. The agent
compares the configuration with the repositories as they are now and with what aw can do now, since
aw may have changed after the configuration was written. It explains what it finds, discusses
alternatives with the developer, and makes only the changes they agree on, on a lane branch, for
review.

## Status

**State:** FULLY IMPLEMENTED

**Updated:** 2026-11-13

**Branches / MRs:** `audit/workspace-audit` (the plan and Phases 1 to 3), rebased on master after
#92 (v0.2.1).

**Done:** Phases 1 to 3, and Phase 4.1. Unit tests, vet, the full acceptance suite (with
`-count=1`), the coverage check, `go mod tidy -diff` and the hot-path check pass locally.
- 1.1 `[workspace] audited_with`, validated as a version. The design and audit skills set it.
- 1.2 `aw changelog [--since] [--json]` (`internal/cli/changelog.go`), over a new
  `upgrade.EntriesBetween`.
- 1.3 `internal/audit`: `Suggest` with the four rules, and `Changelog` with the keys each item
  names. `aw doctor` adds one info line and a `suggestions` array to its JSON.
- 1.4 `[audit] declined`, with the "comes back when the rule is newer than `audited_with`" filter.
- 1.5 `audited_with`, `suggestions` and `changelog_since` in `aw inspect --json`.
- 2.1 to 2.4 `aw audit [--focus] [--harness] [--without-other-hooks]` (`internal/cli/audit.go`), the
  `aw-workspace-audit` skill, and the `audit this workspace` lane purpose, which may rehearse as
  onboarding lanes may. `aw onboard` and `aw audit` share `startConfigAgent`. `aw onboard --review`
  is hidden and starts the audit.
- 3.1 `aw upgrade` prints one line that points to `aw audit` (`audit.Touching`).
- 3.2 and 3.3 Docs (`onboarding.md`, `ux.md`, `getting-started.md`, `upgrading.md`, the config
  reference), the 0.3.0 changelog section, R95 in the spec, `requirements.toml` and the
  release-candidate plan's §2, and R13 pointing to `aw audit`.
- Scenarios `onboarding/audit-suggestions`, `onboarding/audit-starts-a-conversation` and
  `cli/changelog`. `onboarding/onboard-in-a-lane` and `upgrade/edited-managed-file-reported` were
  extended.

Changes from the plan as written:
- Suggestions live in a new `internal/audit` package, not `config.Suggest`. They read
  devcontainer files and run `git check-ignore`, which the config leaf package (on the hot path)
  must not do.
- No `--to` flag. The changelog built into a binary never describes a later version, so it is
  always "up to this aw".
- A suggestion about one project has an id of `<rule>-<project>`, such as
  `devcontainer-local-slot-api`, so declining it for one project leaves the others. Each item
  also carries `rule` and `project`.
- Key extraction accepts every way the changelog already writes a key (`[table] key`,
  `[table.<name>]`, dotted, `key = value`, a key's own name when only one key has it), so the
  changelog needs no new format.
- `aw doctor`'s text output lists the suggestion ids rather than only a count.
- The audit's prompt carries the changelog since `audited_with`, up to 6 KiB, and aw's
  suggestions. When the configuration was never audited it points to `bin/aw changelog --json`
  instead of carrying the whole changelog.
- `audit.Introduced` and the changelog section assume the feature ships in 0.3.0.

**In progress:** Nothing.

**Next step:** Phase 4.2, the owner's dogfood checks on RideTracker, recorded as pending: they need
`~/.local/bin/aw-dev` rebuilt from this branch, which waits for the owner. Then CI on the merge
request, and the 0.3.0 release.

## Decisions taken

1. **The command is `aw audit`**, and the skill is `aw-workspace-audit`. The existing
   `aw-readiness-audit` skill audits the *projects* (what they must change to work in lanes).
   `aw-workspace-audit` audits the *workspace configuration*. Every piece of documentation and help
   text that mentions both says which one is which. `aw doctor --projects` stays the deterministic
   part of the readiness audit (R89).
2. **The stamp is `[workspace] audited_with`** in `workspace.toml`, so `aw doctor` can read it.
3. **Declined suggestions go in `[audit] declined`** in `workspace.toml`, so `aw doctor` stops
   repeating them. Declined ideas that have no suggestion id go in `docs/workspace-design.md`.
4. **An audit that changes nothing is still recorded, when the developer agrees.** The agent asks
   first. If they say yes, it updates `audited_with` and adds an entry to the design doc, and the
   change goes through review. If they say no, nothing is written.

## Why

`aw onboard --review` already starts an agent in a review lane with the `aw-workspace-design`
skill (`internal/cli/onboarding.go`). It falls short of this idea in three ways:

1. **It only compares the configuration with the repositories.** Its prompt compares
   `workspace.toml` with `aw inspect --json` to find new projects, servers and data dependencies.
   Nothing tells the agent what aw has gained since the configuration was written. Examples from
   0.2.0: the generated `.devcontainer/aw/devcontainer.json`, `local_compose`, `aw pull`, and
   lanes that start from origin's default branch. Nothing records which aw version the
   configuration was designed against either, so "since then" has no starting point.
2. **It is a report, not a conversation.** The prompt ends with "then stop and report". The design
   skill asks before it chooses between alternatives the projects already offer, but it gives no
   overview, invites no discussion, and has no step for applying only what was agreed.
3. **It has no memory of what was declined.** `docs/workspace-design.md` records the choices that
   were made. A suggestion the developer turned down is not recorded, so the next audit raises it
   again.

## Principles

- **Agents propose, the tool verifies, a human approves** (`docs/spec/onboarding.md`). The agent
  changes files on the audit lane's branch, or in the owner checkout in a pair session when the
  workspace is not in Git. It never changes them on the default branch. R63 still applies.
- **The tool finds what can be found mechanically, and the agent judges.** A legacy pattern, a
  missing stamp or an unused key that has a better replacement is a deterministic check in aw.
  The agent weighs tradeoffs, explains them and holds the conversation.
- **Don't reopen recorded decisions without a reason.** A choice in `docs/workspace-design.md` is
  raised again only when a newer aw has changed what it rested on, and the agent names the
  changelog entry that did. "Nothing worth changing" is a good result.
- **Rank by payoff.** Speed is the north star: list first what makes lanes start, set up or serve
  faster, then fidelity and safety, then tidiness.
- **This is not on the hot path.** Nothing here touches hooks, lane start or serving, so the
  budgets in `docs/spec/speed.md` are not affected. `aw doctor` must stay within its current time.

## Phase 1: deterministic facts

1.1 **Version stamp.** Add `[workspace] audited_with`, the aw version that last designed or audited
   this configuration (team only, string, semver). The design and audit skills set it to
   `bin/aw --version` as their last change. `aw config validate` checks that it is a version.
   `aw doctor` reports a note, not a warning, when it is missing or older than the running aw's
   previous minor version, and suggests `aw audit`. When it is missing, the audit starts from the
   oldest changelog section and says that it did.

1.2 **`aw changelog [--since <version>] [--to <version>] [--json]`.** This is a thin command over
   `upgrade.Between`, which `aw upgrade` already uses. `--since` defaults to `audited_with`. The
   JSON form lists each entry with its version, whether it is breaking, its text, and the config
   keys it names. Keys are taken from backticked dotted words that parse with `config.ParsePath`
   and that `config.Explain` knows, so the changelog format does not change. The text form is for
   people.

1.3 **Suggestions.** `config.Suggest(ws, c) []Suggestion` returns checks that are deterministic and
   advisory. Each has a stable `id`, the `key` or file it concerns, the `since` version that made
   the alternative available, a message, and a fix. The first catalog is small and limited to cases
   the code already distinguishes:
   - `devcontainer-committed-wiring`: a project with committed aw Compose wiring where aw could
     generate `.devcontainer/aw/` instead (0.2.0);
   - `devcontainer-local-slot`: a devcontainer that lists a gitignored local Compose file which
     `projects.<name>.local_compose` does not name (0.2.0);
   - `audited-with-missing` and `audited-with-old` (1.1).
   Each new rule must cite the changelog entry that introduced its alternative. A rule with no such
   entry is a doctor warning, not a suggestion.
   `aw doctor` prints the number of suggestions and points to `aw audit`. `aw doctor --json` lists
   them. Suggestions never change the exit code.

1.4 **Declined suggestions.** Add `[audit] declined`, a table keyed by suggestion id whose value is
   the reason (team only), shaped like `accepted_findings`. `config.Suggest` leaves those ids out.
   A declined id comes back only when its rule's `since` version is newer than the `audited_with`
   that declined it. In practice that means aw changed the premise, which matches the principle
   above.

1.5 **Facts in `aw inspect --json`.** Add `audited_with`, `suggestions` and `changelog_since` (the
   1.2 JSON) to the inspect output, so the skill reads every fact from one call, as the design
   skill already does.

Tests: unit tests for key extraction from changelog entries, each suggestion rule, and declined
filtering. An acceptance scenario for `aw changelog --since` (text and JSON), and one for
`aw doctor` reporting, and then not reporting, a declined suggestion. Run acceptance with
`-count=1`.

## Phase 2: the audit skill and command

2.1 **`aw audit`.** This is a top-level command that does what `aw onboard --review` does today:
   setup and preflight, a lane with a new `PurposeAudit` (counted with onboarding purposes in
   `rehearsal.go`), the rehearse permission, the notes about hooks that are not aw's, and the
   pair-session fallback without Git. It launches the new skill. `aw onboard --review` stays as an
   alias that prints a one-line pointer to `aw audit`, and is removed at 1.0. Flags: `--harness`,
   `--without-other-hooks`, and `--focus <text>`, which is passed to the agent as the developer's
   opening question ("should we move api to a devcontainer?"). Its help text says it audits the
   workspace's configuration, and points to `aw doctor --projects` for the projects themselves.

2.2 **The `aw-workspace-audit` skill** (`internal/compose/skills/workspace-audit.md`, added to
   `compose.Skills`, composed for each harness like the other two). Its steps, in spirit:
   1. Gather: `bin/aw inspect --json` (1.5), `bin/aw config validate`, `bin/aw doctor`, and
      `docs/workspace-design.md`. Read `bin/aw config explain` for any key you are about to
      discuss.
   2. Brief: open with a short overview in three groups, each item with its payoff, cost and your
      recommendation, ranked by payoff:
      - *the repositories changed*: new projects, servers, data and gitignored files;
      - *aw changed*: suggestions, plus changelog entries since `audited_with` that touch keys or
        features this workspace uses or could use;
      - *worth discussing*: recorded decisions whose premise changed, naming the entry that changed
        it.
      If there is nothing, say so, and offer to record the audit (2.3).
   3. Discuss: wait for the developer. Answer questions, compare alternatives with what each costs
      in speed, memory and fidelity, and change your recommendation when they give you a reason.
      Do not edit anything during the discussion.
   4. Confirm: list what was agreed and what was declined, and ask once to go ahead.
   5. Apply only what was agreed. Write a reason into `[audit] declined` for each declined
      suggestion, and into `docs/workspace-design.md` for each declined idea that has no
      suggestion id.
   6. Verify: run `bin/aw config validate`, `bin/aw doctor`, and exactly `bin/aw rehearse` when
      lanes, servers, hooks or devcontainers changed. Treat rehearsal findings as the design skill
      does.
   7. Record: set `audited_with`, and add a dated entry to `docs/workspace-design.md` under
      "Audits" saying what was considered, chosen and declined.
   8. Summarize, and say that the branch goes through a merge request, as the design skill does.
      Then stay in the conversation. The developer may want to continue.
   Project changes stay options with tradeoffs, as in the design skill. The workspace audit hands
   project readiness work to `aw-readiness-audit` and does not duplicate it.

2.3 **An audit with no changes** (decision 4). The skill asks whether to record it. On yes, it
   updates `audited_with` and adds the design doc entry, which goes through review like any other
   change. On no, nothing is written.

2.4 **Prompt in `internal/cli`.** The launch prompt names the skill, includes the preflight and any
   setup failure (as onboard does), and drops "stop and report".

Tests: an acceptance scenario that `aw audit` creates a lane with the audit purpose, and that the
prompt it passes to a stub harness names `aw-workspace-audit` and includes the changelog section.
Another that `aw onboard --review` still works and prints the pointer. Another that `aw setup`
writes the new skill to `.claude/skills/aw-workspace-audit/SKILL.md` and
`.agents/skills/aw-workspace-audit/SKILL.md`, and that `aw upgrade` refreshes it. The skill's
conversation cannot be tested deterministically, so 4.2 covers it.

## Phase 3: being found at the right time

3.1 **After `aw upgrade`.** When the versions it skipped have changelog entries that name keys the
   workspace sets, or when suggestions are new since the last run, `aw upgrade` prints one line:
   "aw audit can walk through what's new for this workspace (N items)". It does nothing more. It
   doesn't launch an agent.

3.2 **Docs.** Replace "Re-running" in `docs/spec/onboarding.md` with an "Auditing the workspace"
   section, and add a sentence that tells it apart from the readiness audit. Update
   `docs/getting-started.md`, `docs/upgrading.md` (point to `aw audit` after an upgrade), and
   `docs/reference/config.md` (`audited_with`, `[audit]`). Add the release's changelog entry.

3.3 **Requirements.** Add R95: *The framework ships a workspace-audit skill and an `aw audit`
   command that compare an existing configuration with the repositories and with the capabilities
   of the running aw, discuss changes with a human, and apply only agreed changes through review.*
   Amend R13 to point at `aw audit`.

## Phase 4: verification

4.1 Run unit tests, vet and the full acceptance suite locally during the work, and CI once at the
   end of the milestone. Build `~/.local/bin/aw-dev` from the branch first.

4.2 **Dogfood, recorded as pending until the owner does it:**
   - On the RideTracker workspace with `aw-dev`, set `audited_with = "0.1.5"` and run `aw audit`.
     Check that the brief names the 0.2.0 devcontainer changes that apply, that declining one
     writes `[audit] declined`, and that a second audit doesn't raise it again.
   - Run `aw audit --focus "…"` on a question with a real tradeoff, and check that the agent
     discusses it before it edits anything.
   - Run an audit on a workspace with nothing to change, and check that it says so and asks before
     recording.

## Not in scope

- An agent that audits on a schedule or unprompted. An audit is something the developer starts.
- Changing project repositories. That stays with `aw-readiness-audit`, as an option offered to the
  human.
- A machine-readable capability catalog beyond the changelog and `config explain`. If keys tagged
  in the changelog turn out too coarse, revisit after dogfooding.
