# Changelog

Newest first. A line starting with **Breaking:** is called out by `aw upgrade`.

## 0.2.0

- Concurrent lane setup preserves additions to the shared Git exclude file, preventing local aw files from becoming tracked and blocking rehearsal cleanup.

- Host servers started outside aw are detected at their literal `owner_port`, even with a workspace port offset, so they still count toward resource capacity. Devcontainer detection keeps the offset used by aw's published ports.

- Compose devcontainers no longer need committed aw wiring. aw generates an ignored `.devcontainer/aw/devcontainer.json`, preserves the project's initialization and settings, and starts that configuration. Choose its `(aw)` entry in VS Code; Rebuild keeps the choice. aw-published ports are removed from forwarding, and existing local Compose slots and committed aw fragments stay supported.

- Devcontainers carry a configuration hash. When the devcontainer configuration, Compose inputs or aw's mount/ports change, aw replaces an idle service container before reuse and keeps its volumes. Seats, setups, attached sessions or an unreadable usage check refuse replacement with the fix.

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
