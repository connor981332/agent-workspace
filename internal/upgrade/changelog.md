# Changelog

Newest first. A line starting with **Breaking:** is called out by `aw upgrade`.

## 0.2.0

- `aw upgrade`: moves a workspace to a release, shows this changelog, migrates configuration, and regenerates managed files. A managed file edited by hand is never overwritten.
- Homebrew tap, and a Renovate preset.

## 0.1.4

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
