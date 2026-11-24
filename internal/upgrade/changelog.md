# Changelog

Newest first. A line starting with **Breaking:** is called out by `aw upgrade`.

## 0.7.0

- **Breaking:** with `[user] multiplexer` set in your individual layer, `aw new` opens the lane in a new tab or window by default and leaves this terminal free, so one shell at the workspace root can start several lanes. `aw new --here` runs it in this terminal, as before. Inside tmux or herdr with no multiplexer set, `aw new` says how to set one. `aw resume` and `aw pair` still need `--mux` to open in the multiplexer.
- `multiplexer = "herdr"` works with Herdr 0.1.16 and later, which install Herdr Core's `hc` and no `herdr` command: aw opens the lane with `hc run` in a new tab in this terminal's Herdr space, and says when no Herdr tool is found or the terminal is not a Herdr one. A lane agent may use `hc` only to look, as with `herdr`.
- `aw sweep` says what keeps a lane: held, unmerged, stuck or protected. It names every session holding a lane, with its pid and terminal, and how to end each one; `--json` keeps the action `keep` and adds `kept` and `holders`. A lane held only by sessions, with every repository clean and proven merged, says it will be recycled once they end, and a recycled lane says it was recycled and its place in the pool.
- A shell started by `aw new --shell` that has left its lane's directories is marked idle in `aw sweep` (`holders[].idle` in `--json`), and the next step names its pid and terminal, how long it has held the lane, and that exiting it releases the lane.
- When a repository is not proven merged against a base last fetched more than an hour ago, `aw sweep` says when it was fetched and starts the lane's next steps with `aw pull`. `--json` gives the time as `last_fetch`.
- `aw sweep` prints its progress on stderr: each lane it acts on and each step, and, for a step that takes a while, that it is still at it with the elapsed time. stdout keeps the report, and a quick preview prints nothing.
- `aw new` started from inside another lane's session warns which lane's session it is, whether that session still holds its lane, and how to leave it first or use `--mux`.
- A multi-line Git error relayed by `aw sweep`, `aw discard --orphans` or onboarding keeps its line breaks, with Git's lines indented under aw's own.
- A usage error prints the command's usage line, and `aw new` without a purpose says it needs one, with an example.
- `aw --version` prints what `aw version` prints.

## 0.6.0

- `aw sweep` reports a lane whose work is pushed and waiting in review as `reclaim`, not `keep`: no session holds it, it has no open notes, and every repository is clean and either proven merged or in an open merge request that the forge reports at exactly the repository's head. `aw sweep --pushed --apply` archives its branches under `refs/aw/archive`, as `aw discard` does, and removes it; plain `--apply` leaves it alone. A maintenance delegation does not cover `--pushed`.
- `aw rehearse` says when a lane's setup is waiting on an unmet prerequisite, with the prerequisite's fix, as soon as it starts waiting and again every minute, instead of printing nothing until it is met. A step stopped by the rehearsal's time limit says so, and the teardown after it gets its own time.
- `accepted_findings` keys of `"**"`, `"**/*"` and `"/**"` now accept every file in a project at any depth in `aw doctor --projects` and `aw inspect`, as the 0.5.0 notes said; before, they matched one directory level, and `"/**"` failed validation.
- Removing a lane no longer fails with "not fully merged" when the owner checkout is on a branch behind the default branch: aw deletes the branch once its own merge proof holds, whatever the owner checkout is on.
- `aw sweep --apply --retry` finishes a removal that stopped after taking some of a lane's worktrees, instead of refusing it as a worktree on a detached HEAD. A removed worktree whose branch has unmerged work still keeps the lane.

## 0.5.0

- **Breaking:** a lane checks a project out on first use, as it already set one up on first use. A new lane has only the projects in the new `[lanes] eager` list, those `aw new --projects` or `aw attach --projects` name, and what they `depends_on`. `bin/aw use <project>` checks out another, on the lane's branch, and `aw run`, `aw serve` and team commands do it for you. Until then the guard redirects a path under a missing project to `bin/aw use`. A workspace whose agents expect every project in every lane sets `eager`, or keeps `[lanes] setup = "eager"`, which still means every project at claim time. The workspace's own `prepare` and data providers run before most projects are checked out, so one that reads a project's directory needs that project in `eager`. `aw audit` proposes `eager` and `depends_on`.
- `aw lanes` shows which projects each lane has checked out, and the generated instructions list the workspace's projects and `bin/aw use`. A recycled lane keeps its worktrees, and the next claim puts each one on the new lane's branch.
- The guard no longer asks the human when a lane's agent reruns its own lane's setup with flags, such as `aw setup <lane> --project <name>` (which aw's own note suggests), `--project=<name>` or `--json`, or names the lane by its number (`1` for `01`). Every command aw tells an agent to run is now tested against the guard.
- `aw sweep --apply` closes an unfinished cleanup whose lane no longer exists and has no worktree in any repository, instead of reporting it forever; `aw sweep --apply --retry <id>` on such an entry does the same. The report shows one line per unfinished cleanup, and `aw sweep --json` keeps the full detail.
- `generated`, `local_files` and `accepted_findings` patterns follow `.gitignore`'s rules everywhere: `**` spans any number of directories wherever it appears (not only at the end), a pattern that matches a directory covers everything in it, and a leading `/` anchors a pattern without another slash to the project's root. `"**" = "…"` under `accepted_findings` now accepts every file in a project.
- `aw rehearse`, `aw inspect` and `aw doctor --projects` no longer read a project's declared `secrets` and `local_files`, their backups (such as `.env.bak`) or its gitignored files, so their findings never come from a file aw keeps agents from reading.
- A repository's own Git hook that fails in a lane is reported as that hook's failure, with its exit status passed on to Git, instead of "so this call is refused → run bin/aw doctor".
- `aw new` no longer makes Claude Code ask to trust each project's repository. aw stopped passing `--add-dir`, which also loads a project's own skills and commands; the owner checkout's `.claude/settings.local.json` lists each project's `.worktrees` directory instead, which gives file access only. `aw setup` writes the entries, the next lane claimed adds any an older aw left out, and `aw doctor` warns when they are missing or the file is tracked by Git. A project's own `.claude/skills`, commands and agents no longer load in a lane session started by `aw new`.
- `aw rehearse` reports a resource as skipped, naming the port, when your own server holds one of its seats and the two lanes cannot both be served, instead of calling it a collision. "(a fixed port?)" appears only when the lane was not queued.

## 0.4.0

- **Breaking:** a session's identity is set when it starts and changes only through aw's commands, never through its working directory. `aw detach` returns a session to what it was before `aw attach`; `aw pair --session` always succeeds.
- `aw finish` releases the lane and writes the handoff. A session that ends, or whose process dies, releases its lane too, and `aw` shows a lane that ended without a handoff. Lane states (in progress, ready for review, idle, recyclable) are derived rather than stored; parked stays stored.
- **Breaking:** a lane sets up a project on first use (`aw run`, `aw serve`, a team command) rather than at claim time. `[lanes] setup = "eager"` keeps the old behaviour, and `aw setup <lane> --project <name>` sets up one project.
- A claim that finds every lane in use first recycles the lanes nobody holds that are proven merged, as `aw sweep --apply` would.
- `aw audit` and `aw onboard` run as pair sessions in the owner checkout by default; `--lane` starts one that works alone in a lane.
- Guards are sorted into tiers. Force pushes, deleting branches, discarding work, `git config`, `git remote`, stopping processes, changing shared containers and reading secret files now ask the human in the harness (where the harness can ask) instead of refusing. Reads from the wrong directory go through with a note. Recovery commands (`aw doctor`, `git status`, `git switch`) are never refused, even with a broken configuration.
- `[prerequisites.<name>]` with `check`, `fix` and `message`, listed by projects under `prerequisites`: checked once before a setup that needs it, cached briefly, and dependent setups wait for the human to run the fix. `aw doctor --prerequisites` runs them.
- A fourth merge proof, by content: merging the lane's head into the base changes nothing (Git 2.38 or later). Every recycle not proven by ancestry archives the tip under `refs/aw/archive` first; `aw archives` lists the tips and `aw archives --restore <name>` restores one. `[lanes] archive_retention` (default 30 days) prunes them.
- `aw sweep --apply <lane>…` aims at named lanes, and every "not proven merged" reason lists the checks it tried.
- Output is structured on a terminal (live steps, colour, a summary of what needs you) and plain everywhere else, including agent sessions. Long step output goes to a log under `.git/aw/logs/`, whose path the error names.
- Team hooks in a lane no longer inherit `GIT_DIR` and `GIT_WORK_TREE`, and a recycle never runs a repository's own post-checkout hook. A failed recycle records each repository it reset, and a retry continues from there.

## 0.3.0

- `aw audit` starts an agent that talks through the workspace's configuration with you: what changed in the repositories, what this aw can do that the aw it was written for could not, and what `aw doctor` suggests. It changes nothing until you agree, and then only what you agreed, on a lane's branch for review. `aw onboard --review` now starts the same audit.
- `[workspace] audited_with` records the aw version that last designed or audited the configuration; the design and audit skills set it. `aw changelog` lists what changed since then, and its `--json` names the configuration keys each item touches.
- `aw doctor` lists suggestions for the configuration, such as a gitignored local Compose file that `projects.<name>.local_compose` could name, or committed aw devcontainer wiring that aw can now generate. They never fail the doctor. `[audit] declined` keeps one from being raised again until a newer aw changes it. `aw inspect --json` carries the stamp, the suggestions and the changelog since.
- `aw upgrade` mentions `aw audit` when the versions it skipped touch keys the workspace sets, or there are suggestions.
- `aw rehearse` in the owner checkout no longer passes the caller's session to its lanes. A paired agent that ran it got the same lane twice and lost its pairing.
- `aw upgrade` and `aw setup` no longer call an onboarding skill "edited by hand" when it holds a version aw shipped but `.aw/managed` has no hash for it, and they record a missing hash for a skill that is up to date.
- `aw rehearse`, `aw inspect` and `aw doctor --projects` no longer report the ports in files aw generates, such as `.devcontainer/aw.compose.yaml`, or the localhost URLs in agent harnesses' settings, as fixed ports.
- A lane's setup no longer blocks its own replacement of a devcontainer built from an older configuration. Another lane's setup protects the container only once it has used it; seats, attached sessions and an unreadable usage check still refuse.
- `aw config explain` and `aw config get` given a key without its tables, such as `accepted_findings`, name where it belongs: `projects.<name>.accepted_findings`. The reference and the onboarding skills now show an example of `accepted_findings`: under `[projects.web.accepted_findings]`, a line such as `"ci/**" = "CI has its own services"`.
- `aw finish` hands off a lane whose setup did not complete, with a warning naming `aw setup <lane>`, instead of refusing. Commands that need the failed step still refuse until it is retried.
- In a lane, `aw options`, `aw config get` and `aw config explain` read the lane's `workspace.toml`, as `aw config validate` and `aw doctor` do, so a proposed option can be previewed before it is merged.
- `aw config validate <file>` checks every option choice too, as it does for the workspace: a choice nobody picks may no longer name a server that does not exist.
- `aw inspect` names the tools a hook runs, rather than its first word: `(bundle check || bundle install)` no longer reports the tool `(bundle`.

## 0.2.1

- The 0.2.0 notes said a replaced devcontainer keeps its volumes; only named volumes are kept. The replacement message now says so, and the 0.2.0 notes now call out that devcontainers built by an earlier aw are replaced once, when idle, after upgrading.

## 0.2.0

- **Breaking:** devcontainers built by an earlier aw have no configuration hash, so each is replaced once, when idle, on first use. Named volumes are kept; anonymous volumes and changes made inside the container are not.
- Concurrent lane setup preserves additions to the shared Git exclude file, preventing local aw files from becoming tracked and blocking rehearsal cleanup.

- Host servers started outside aw are detected at their literal `owner_port`, even with a workspace port offset, so they still count toward resource capacity. Devcontainer detection keeps the offset used by aw's published ports.

- Compose devcontainers no longer need committed aw wiring. aw generates an ignored `.devcontainer/aw/devcontainer.json`, preserves the project's initialization and settings, and starts that configuration. Choose its `(aw)` entry in VS Code; Rebuild keeps the choice. aw-published ports are removed from forwarding, and existing local Compose slots and committed aw fragments stay supported.

- Devcontainers carry a configuration hash. When the devcontainer configuration, Compose inputs or aw's mount/ports change, aw replaces an idle service container before reuse. Named volumes are kept; its anonymous volumes and anything changed inside the container are not. Seats, setups, attached sessions or an unreadable usage check refuse replacement with the fix.

- Devcontainer projects may set `local_compose = "compose.local.yaml"` to write aw's mount and seat ports into a local Compose file their committed configuration already lists. The editor's `workspaceFolder` may stay as it is, or be omitted: aw mounts the checkout at the same path separately and runs commands at lane paths.

- **Breaking:** a new lane starts from origin's default branch as last fetched, not from the owner checkout's branch, so a lane no longer carries the owner's unpushed commits or other branch. `aw new` says when the owner checkout differs, or when origin was last fetched over a day ago. A workspace repository with unpushed commits refuses to start a lane until they are pushed, or `--branch` names the branch to start from.
- `aw pull` fetches origin in every owner checkout and fast-forwards those on the default branch. It never merges, rebases or switches branches.
- A lane may read anywhere, including the owner checkout, and only changes to files outside the lane are refused. Reading the owner's copy of a file the lane has too comes with a note naming the lane's copy. A read-only command is no longer refused for a path aw cannot resolve (unless the `secrets` safeguard is on), and variables, `for` loops over literal words and functions defined in the same command are followed. `gh api` and `glab api` endpoints are not checked as file paths.
- `aw push [--as <name>]` pushes a lane's branch under the team's branch name and remembers it, so recap, finish and sweep find the merge request. Renaming or making a branch in a lane is refused up front, and a lane whose branch went missing is told how to get it back.
- A pair session acts for you: aw's own rules no longer apply to it, so it may change the workspace's configuration and run your `aw` commands, such as `aw upgrade` and `aw discard`. Your team's guards and the safeguards you turned on still apply.
- `aw new --shell` and `aw resume <lane> --shell` set up a lane and start your own shell in it, with the lane's environment, instead of a harness.
- `aw doctor` warns when the owner checkout would configure lanes from something other than origin's default branch. In a lane, `aw config validate` and `aw doctor` check the lane's own `workspace.toml`. `aw recap <lane>` shows one lane, `aw discard` says what it would archive before asking for `--yes`, `aw inspect` lists repositories that are not projects yet, and `aw add project` in a lane proposes a project for review.
- `aw attach --wait` waits for the lane's setup and ends with its result; `aw serve` says its place in line and each server it starts, with its port and log.
- `aw setup <lane>` links the secret files and copies the local files the configuration declared since the lane was made, and says when a project is missing from the lane.
- `aw reset-branch` starts a lane over from its base, archiving each tip first, and `aw attach` says when a session's lane still holds an earlier purpose's commits.
- `aw note "<text>"` keeps a note on the lane of something left to do outside it, such as a temporary CI schedule. `aw recap` and a session starting in the lane show open notes, `aw finish` warns, and `aw sweep` keeps the lane until they are done.
- The devcontainer runner uses the Compose project name the project's own configuration gives (`COMPOSE_PROJECT_NAME`, or a top-level `name:`), where it used `<folder>_devcontainer` regardless. With a project that sets its own name, aw now joins the developer's existing stack instead of starting a second, empty one beside it.
- `[options.<name>]` declares alternative setups the team supports, each choice a reviewed overlay of configuration keys, and `[choose]` in your individual file picks one. `aw options` lists them, `AW_OPTION_<NAME>` tells hooks and commands, `aw config validate` checks every choice, and a team can lock one with `locks.keys = ["choose.<name>"]`. `{lane}` and `{namespace}` are now filled in in `projects.<name>.env` too.
- `[resources.<name>] on_acquire` runs a command each time a process resource's seat is handed to a lane, before its servers start, such as resetting a shared Kafka consumer group for a single app slot.
- `aw upgrade --take` replaces every file it reports, and rehearsal skips the Compose files listed in `accepted_findings`.
- `aw recap` shows only a lane's own work: repositories the lane has not changed collapse into one "no changes" line, a commit already on origin counts as pushed, and the workspace files aw copied into a lane are not counted as uncommitted work.
- `aw upgrade`: moves a workspace to a release, shows this changelog, migrates configuration, and regenerates managed files. A managed file edited by hand is never overwritten.
- Homebrew tap, and a Renovate preset.

## 0.1.5

- `aw upgrade` ignores `AW_TOOL_PATH`: the new release is downloaded, verified and finishes the upgrade itself, so a local build never regenerates the managed files. The finishing step refuses to run as any version but the pinned one.
- `aw sweep` looks inside an ignored directory that holds a declared generated path, so `lib/generated/` with only `lib/generated/prisma` in it no longer keeps a lane.
- `[workspace] generated` declares the workspace repository's own generated paths.
- `aw sweep` ends with what would let each kept lane be recycled, and its `--apply` hint asks whether the preview looks right.
- `aw lanes` ends with how to continue a lane: `aw resume` for one whose last session can be reopened, and `aw open` for a fresh session from the handoff.

## 0.1.4

- **Breaking:** aw now regulates only its own lanes, seats and state. Guards on destructive Git, publishing, secrets, processes and containers are off unless you turn them on with `[safeguards] enable = ["git-safety", "publication", "secrets", "processes", "containers"]` in `workspace.toml`, so add that to keep them.
- `aw init` no longer pre-authorizes publishing, and generated instructions mention publishing only when that safeguard is on.
- `aw onboard` keeps hooks that are not aw's in its lane. `--without-other-hooks` replaces `--keep-other-hooks`.
- A command aw cannot read is refused only when a rule in force depends on it.
- `aw rehearse` now checks every project's Compose files and custom data providers, not only `infra/` and devcontainers, and refuses a proposal that could reach real containers or data. A host project's Compose files get a project name of their own in a rehearsal.
- Custom data providers are told a rehearsal's names, and the object-store module's provider uses them. Reinstall the module to get this: an older copy now stops the rehearsal.

## 0.1.3

- Where a person can answer, human-only commands and destructive Git in a lane now ask instead of refusing. Force pushes, deleting branches and secrets stay refused.
- `aw init` pre-authorizes pushing a lane's own branch, opening a merge request, and commenting on or merging the lane's own. Add `[publication] allow` to an existing workspace to get the same.
- The generated instructions tell agents to ask the human to approve work they were asked for, rather than stop.
- `aw new`, `aw pair` and `aw onboard` give Codex the writable paths directly, because it ignores an untrusted project's configuration.
- New lanes tell Claude Code their project worktrees are working directories, so it stops asking about each one.
- A commit message or merge request title with a slash in it is no longer read as a path.
- Where publishing is pre-authorized, a command the agent can correct itself, such as a merge by number, is refused with the command that works instead of asking the human. The generated instructions say how to publish a lane's work.
- `build_template` commands get `AW_OWNER`.
- `aw attach` uses the harness's own session ID, so a session started by `aw new` is bound to the lane it claims, also after `/clear`.
- `aw setup <lane>` reinstalls when the install's stamp has changed, such as a lockfile a merge brought in.
- `aw run` warns when a lane has no `node_modules` and would use the owner checkout's.
- Making a new branch in a lane is refused before Git touches the files, instead of after.
- A key placed after a string table gets an error that says to move it.

## 0.1.2

- `aw onboard` lets its lane's agent run `bin/aw rehearse` without a Claude Code permission prompt, through one allow rule in that lane's own gitignored settings.
- The onboarding skill runs the rehearsal exactly as that rule expects.
- `build_template` builds a Postgres template the first time it is missing, once, however many lanes start together.
- `accepted_findings` marks readiness findings a person has reviewed, with the reason, so they no longer fail a rehearsal.
- A rehearsal says exactly which files it wrote, and warns if your real checkout changed meanwhile.
- The Postgres provider waits out a new container's restart. `--help` works on the human's commands from an agent session.

## 0.1.1

- `aw rehearse <lane>`, or `aw rehearse` from inside a lane, tests a lane's proposal in a throwaway copy with its own containers, without touching your checkout. The agent onboarding a workspace can run it for its own lane.
- `aw onboard` turns off hooks that are not aw's in its own lane, and says which. `--keep-other-hooks` leaves them on.
- `aw try` works before the workspace's files are committed: aw's own setup files are kept safe and put back. Lanes no longer make a project look dirty.
- A lane gets aw's edits to files the commit already tracks, so it has aw's guards before they are committed.
- `aw discard --orphans` leaves worktrees that are not numbered lanes alone.

## 0.1.0

- `aw onboard` is the one command to start with: it includes `aw setup`, needs nothing committed, shows progress, and hands any failure to the agent.
- A workspace can be plain files, with only its projects in Git.
- A repository's post-checkout hook no longer stops a lane: it runs as a retryable setup step. A failed lane is rolled back, and `aw doctor` and `aw discard --orphans` deal with what older failures left.
- `local_files` and `[projects.<name>.env]` declare a project's gitignored files and settings. `aw inspect` reports checkout hooks, missing files and missing tools.
- Sessions with no lane can run read-only Git and curl, are asked about what aw cannot classify, and may write outside the workspace. A standalone shell assignment no longer crashes the guard.
- `aw lanes` lists the lanes. `aw delegate` needs no arguments. `aw setup` no longer creates `CLAUDE.md`.
- First release a team can use daily: lanes, merge proof, guards for Claude Code, Codex, Grok and OpenCode, the resource queue, lifecycle hooks, providers, devcontainers, signed releases, and `aw setup` from a fresh clone.
