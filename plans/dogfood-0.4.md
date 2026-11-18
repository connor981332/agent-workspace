# Dogfood 0.4 plan

How aw responds to feedback gathered while dogfooding aw 0.4.0 in the large multi-repository work
workspace (about 15 projects, macOS, Claude Code). There were three notes: a bug report and an
audit write-up, both by agents, and one from the owner about `aw new`. The owner and an agent
triaged them on 2026-11-17 into noise, bugs and two questions of direction. Both questions were
settled the same day, one of them with an experiment against Claude Code. This plan turns the
result into work.

## Status

**State:** PARTIALLY IMPLEMENTED

**Updated:** 2026-11-17

**Branches / MRs:** #107 (this plan and Phase 1), `feat/dogfood-0.4-phase2` (Phase 2).

**Done:** triage, decisions and the Claude Code trust experiment (below). Phase 1, each item with a
scenario or table test that fails on master first. Differences from the plan as written:
- 1.1: a lane given by number also matches its directory without the leading zero. The only flags
  allowed with a lane are `--project` and `--json`; others (`--containers`, `--harness`) still ask,
  since they mean nothing for a lane.
- 1.2: the table is curated, in `internal/policy/suggestions_test.go`, rather than scraped from the
  messages: the code's `bin/aw …` strings mix agent-facing hints with prose and commands addressed
  to the human. It passes with the default safeguards; with the opt-in `publication` safeguard,
  `aw push` is the guard tier on purpose. Nothing else needed fixing.
- 1.3: sweep without `--apply` names a stale entry and says `--apply` closes it, since a preview
  writes nothing.
- 1.4: `evidence.Declared` itself was wrong in the same way, so the fix is there: `**` spans
  directories anywhere, a pattern that matches a directory covers what it holds, and (as in
  `.gitignore`) a leading `/` anchors a pattern without another slash. `generated`, `local_files`
  and `accepted_findings` share it.
- 1.5: findings already named only the match (`localhost:3000`), never the line, so nothing echoed
  a credential; the fix is that the files are not read at all. The rehearsal's isolation walk skips
  the same files.
- 1.6: already covered by `sweep/recycle-runs-no-checkout-hook` (dogfood-0.3 item 1.2).
- 1.7: statuses 126 and 127 (the binary could not run) still get the refusal message.
- The changelog had no 0.4.0 section, so `aw upgrade` from 0.3.0 showed no notes; one was added.
  The audit's prompt, which carries 6 KB of the changelog, now puts breaking changes first, so that
  the longer changelog doesn't push one out.

Phase 2, except the manual check 2.5. Differences from the plan as written:
- 2.2: Claude Code reads a worktree session's local settings from the main checkout, so the entries
  go in the owner checkout's file only, not Codex's. A claim also adds missing entries when that
  file exists, so a workspace set up by an older aw needs no `aw setup` first; it never creates
  the file. aw creates each project's `.worktrees` directory, so the entry names a real directory.
- 2.3: the doctor check is "Claude Code directories". It warns, rather than failing, as the other
  harness checks do.
- 2.4: nothing to change: Codex's writable roots already name each project's worktree for the
  lane, whether or not it exists yet.
- 2.5 is pending: the owner runs `aw new` with the dev binary in the work workspace on the Mac.

**In progress:** nothing.

**Next step:** Phase 3, starting with its design note (3.1).

## Already done, or not acted on

- A recycle running a repository's own post-checkout hook although `post_checkout = "skip"`: that
  was 0.3.0. Since dogfood-0.3 item 1.2, recycle switches with hooks off
  (`internal/lanes/sweep.go:485`). Only the regression test is left (1.6).
- "The third recycle was marked failed although its hook succeeded": also 0.3.0, on a path that no
  longer runs the hook.

## The trust experiment

Run on 2026-11-17 with Claude Code 2.1.327, in a throwaway copy of aw's layout: a trusted workspace
repository with a lane worktree, two project repositories with worktrees, and links from the lane
root. `acceptEdits` mode, so the only prompts left are about directories.

| Case | Setup | Result |
|---|---|---|
| A | `--add-dir <project worktree>`, as `aw new` does today | The owner's prompt word for word, although the workspace was trusted. Its answer "holds for this session". |
| B | No flag; `permissions.additionalDirectories` in the owner checkout's untracked `.claude/settings.local.json` | No prompt at start. Read, Edit and a shell `cat` through the link: no prompts. The project repository stays untrusted. |
| C, control | Edit a second project that isn't listed | Prompt ("outside working directory … via a symlink"), so the boundary is active |
| C | The second project added to the settings file mid-session | The edit goes through, with no prompt and no restart |

This matches Claude Code's documentation. Trust is keyed on a repository's root, and a worktree
counts as its main checkout. A flag-added directory also loads that directory's skills, commands
and agents, which is why it needs trust. A directory listed in settings grants file access only.
Settings files reload while a session runs.

## Decisions taken

Each decision applies a principle from AGENTS.md; the number in brackets is the principle.

1. **`aw new` causes no trust prompts** [5, 3]. aw stops passing `--add-dir`. Claude Code reaches
   lane worktrees through `permissions.additionalDirectories` in the owner checkout's untracked
   `.claude/settings.local.json`. aw never writes Claude Code's trust state (`~/.claude.json`):
   trusting a repository is the human's decision. The cost, accepted: a project's own
   `.claude/skills`, commands and agents don't load in a lane session. An opt-in can add that
   later if a team needs it, and it would then cost one trust prompt per repository, which is the
   honest price of running a repository's commands.
2. **The settings list each project's `.worktrees` directory, not each lane's worktree** [5]. One
   entry per project covers every lane, present and future, including worktrees created lazily.
   aw then writes the file when the project list changes (onboard, setup, upgrade), not on every
   claim. That keeps aw's writes away from the rules Claude Code saves to the same file. Lane
   boundaries stay the job of aw's guard, not of Claude Code's directory boundary.
3. **Worktrees are created on first use** [5]. Claiming a lane creates the worktrees of the
   projects listed in `[lanes] eager`, and of any the human names with `aw new --projects`.
   `bin/aw use <project>…` adds others to the caller's own lane. Any agent may run it in its own
   lane without a prompt; it is quick and shows progress. `aw run` and `aw serve` check a project
   out first if needed, as they already set it up first.
4. **The guard redirects to `aw use`** [2, 6]. A read, write or command under `<lane>/<project>`
   for a project not checked out in the lane is refused with "web isn't checked out in lane 03 →
   run bin/aw use web". That's the redirect tier: the agent fixes it with one command, and the
   human isn't asked.
5. **One list for eager projects** [7]. `[lanes] eager = ["<project>", …]` checks out *and* sets
   up the listed projects at claim time. Everything else is lazy for both. `setup = "eager"` stays
   as shorthand for every project. If a case for "checked out but not set up" shows up while
   dogfooding, the two can be split then.
6. **Dependencies come with a project** [2]. `[projects.<name>] needs = ["<project>", …]`: using a
   project checks out what it needs first, recursively. Onboarding and audit propose `needs` and
   `eager` entries.
7. **Every command aw tells an agent to run passes the guard without a prompt** [2, 6]. A hook's
   "ask" overrides the user's `settings.json` allow rules, so a wrong ask is a dead end the user
   can't fix.
8. **aw's scans never read what aw forbids agents to read** [8, 4]. Scans skip declared `secrets`,
   `local_files` and gitignored files, and never echo a line's contents.

## Phase 1: bugs

Independent of the phases below. Each item gets a scenario or a table test that fails on master
first.

1.1 The guard asks about `aw setup <own lane>` once it has flags (`--project X`, `--project=X`,
    flags before the lane, `--json`), or when the lane is given as a number without its leading
    zero (`1` for `01`). The guard's own warn note (`internal/policy/rules.go:1375`) suggests
    exactly `bin/aw setup <lane> --project <name>`. Parse `setup`'s flags and their values in
    `ownLaneSetup` (`rules.go:432`), and resolve the lane argument as the command does (name,
    directory, number). Keep asking when no lane is given and for another session's lane.
1.2 Decision 7, as a test: collect the commands aw prints to agents (`→ run …`, `bin/aw …` in
    `internal/policy` and the lane, setup and sweep messages) into a table, and run each through
    `aw hook claude pre-tool-use` as the session it would be shown to. Every one must be allowed.
    Fix whatever else it finds.
1.3 `aw sweep` can't close unfinished journal entries whose lane no longer exists; `--retry <id>`
    exits 0 and changes nothing (`internal/lanes/sweep.go:95–170` only acts on lanes that exist).
    When an entry's lane isn't in state and no repository has a worktree for it, sweep closes the
    entry itself and says what remains (the branch, any `refs/aw/archive` tips). `--retry` on such
    an entry does the same. The report shows one line per unfinished entry plus its log path,
    instead of the full hook output each time. No `--forget`: aw can prove the entry is stale, so
    it fixes it.
1.4 `**` in `accepted_findings` matches at any depth only as a trailing `/**`; elsewhere it acts
    like `*`, and a directory key (`subdir`, `subdir/`) matches nothing
    (`acceptedFinding`, `internal/lanes/rehearsal.go:380`). Use the matcher `generated` and sweep
    already share (dogfood-0.3 item 1.8), so `**` means any number of segments everywhere and a
    directory key covers the directory. The same goes for `aw doctor --projects`. The table test
    uses the cases from the report.
1.5 Decision 8: rehearsal's and `doctor --projects`' address and port scans skip declared
    `secrets`, `local_files` and gitignored files (using `git ls-files --others --ignored`, as
    `internal/inspect/inspect.go:236` does), and a finding gives `file:line` and the kind of match,
    never the line. Check `inspect`'s other scans (`secret-logged` and the like) the same way.
1.6 Regression test: a recycle doesn't run a repository's own post-checkout hook, whatever
    `post_checkout` says.
1.7 The launcher reports a git hook's failure as "exited with status N, so this call is refused →
    run bin/aw doctor" (`internal/launcher/launcher.sh:37`). In `git-hook` mode, pass the status
    through and name the hook that failed ("mtg-ios's own post-checkout hook exited 1"). Keep the
    refusal wording for harness hooks.
1.8 Rehearsal reports a seat held by the owner's own server as a collision (`internal/cli/
    onboarding.go:803`). Before serving a resource, count the free seats (capacity minus the
    owner's when `owner_counts`). With fewer than two free, report it as skipped, not failed:
    "resource X: the owner's server on port N holds 1 of 2 seats → stop it and rerun aw rehearse
    to test two lanes". Rehearse doesn't wait for the human. The "(a fixed port?)" hint appears
    only when the error is a port conflict, not a queue position.

## Phase 2: no trust prompts

Decisions 1 and 2. Small, and it can ship before Phase 3. It changes what a session can reach, so
it ships on its own.

2.1 Remove `launch.DirArgs` and its call (`internal/cli/run.go:176`), and update the scenarios
    that expect `--add-dir` (`harness/mux-tmux`, `lanes/new-passes-harness-args-verbatim`).
2.2 `sandbox.Install` lists `<owner>/<project>/.worktrees` for every project with lanes in
    `permissions.additionalDirectories`, next to the `.git` entries it writes today. It runs from
    onboard, setup and upgrade, so existing workspaces get the entries on their next `aw setup` or
    `aw upgrade`. It never removes entries the human added.
2.3 `aw doctor`: `sandbox.Missing` reports a project whose `.worktrees` entry is missing, and gives
    `bin/aw setup` as the fix. A doctor finding also warns when `.claude/settings.local.json` is
    tracked by Git, since Claude Code then holds its entries until the folder is trusted.
2.4 Codex: `launch.SandboxArgs` passes the projects' `.worktrees` directories as writable roots
    too, so lazily created worktrees (Phase 3) are covered there as well.
2.5 Re-run the experiment as a manual check on the owner's Mac, with a real workspace and the dev
    binary (`~/.local/bin/aw-dev`): `aw new` in a workspace whose projects Claude Code has never
    trusted shows no prompts. Recorded as pending until the owner has done it.

## Phase 3: lazy worktrees

Decisions 3 to 6. Depends on Phase 2, which already covers worktrees created mid-session.

3.1 A design note first, in `docs/spec/sessions.md` (next to "Lazy per-project setup") and
    `docs/spec/architecture.md`. It covers: what a claim creates; `aw use`; the redirect; `needs`;
    what workspace-level `prepare` steps and data providers may assume about which projects exist;
    the warm pool (R24), which pre-builds only eager projects; and how `aw lanes`, sweep and
    rehearse treat a lane with only some projects. It amends **R21** ("a worktree in every project
    configured for lanes"), naming the new wording, and is reviewed before code.
3.2 Schema and reference: `[lanes] eager`, `projects.<name>.needs`, and `setup = "eager"` as
    shorthand for every project. Validation: unknown project names, and cycles in `needs`.
3.3 Claim creates worktrees, links, secret links and copies only for eager and named projects;
    `aw new --projects a,b` names them.
3.4 `bin/aw use <project>…`: idempotent, own lane only, pulls in `needs`, shows progress, and says
    what it added. Setup of the added projects stays lazy unless they are eager. `aw run` and
    `aw serve` call it first when the project isn't checked out.
3.5 Guard: the redirect from decision 4, for file tools and for shell commands that name a path
    under a missing project. It stays read-only and within the latency budget (`docs/spec/
    speed.md`). `aw use` on the caller's own lane is allowed silently.
3.6 Sweep, merge proof, discard and recycle work over the repositories a lane actually has
    (already stored per lane; check the paths that walk all configured projects). A recycled lane
    returns to the pool with only its eager projects.
3.7 The generated instructions list the workspace's projects and `bin/aw use`. `aw lanes` shows
    which projects each lane has checked out.
3.8 `aw rehearse` exercises the lazy path: claim, `aw use` one project with a `needs`, run its
    smoke command, recycle.
3.9 Onboarding and audit skills: propose `eager` for the projects most tasks touch, and `needs`
    from what the builds depend on. Check whether `prepare` steps or data providers assume every
    project is present.
3.10 Upgrade note: lazy becomes the default, so a workspace whose agents rely on every project
    being present sets `eager` (or keeps `setup = "eager"`). `aw upgrade` names this in its notes.

## Verification

Local checks during a phase: unit tests, vet, and acceptance with `-count=1` and
`TMPDIR=~/.cache/aw-tmp`. Then CI once per phase, and a release when the owner wants to use it.
Phases 1 and 2 can each be released alone. Phase 3 is used on the dev binary in the work
workspace before a release. The manual checks (2.5, and `aw new` / `aw use` on the Mac for Phase
3) are recorded as pending until the owner has done them.

## Not in scope

- `aw sweep --forget <id>`: 1.3 closes stale entries itself, since aw can prove they are stale.
- Writing Claude Code's trust state (`~/.claude.json`) for the human: trust is the human's
  decision, and decision 1 makes it unnecessary.
- Moving project worktrees under the lane root: trust is keyed on the repository, so it wouldn't
  remove a prompt, and the current layout serves devcontainers (architecture.md).
- Loading projects' own skills, commands and agents in lane sessions: a later opt-in, if a team
  asks.
- Separate eager settings for checkout and for setup: not until dogfooding shows a need.
