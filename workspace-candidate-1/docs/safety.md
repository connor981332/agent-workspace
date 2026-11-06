# Safety model

## What is enforced

With hooks installed, each tool call an agent makes is checked first.

- File tools and literal paths in shell commands must stay inside the agent's lane. Paths outside the
  workspace, such as `/tmp` or `~/.claude`, are allowed.
- Commands that kill processes, manage containers or start servers directly are refused.
- Owner-only commands are refused, including inside wrappers named in `[tool] shims`.
- If a guard cannot run, for example because `workspace.toml` is unreadable, the call is blocked.

## What is not enforced

Hooks are guardrails for cooperative agents.

- Shell text is parsed, not executed. `cat "$(printf '/some/path')"` is not seen as a path.
- A harness without hooks is not confined at all, so it has to follow `AGENTS.md`.
- On the host runner, every lane runs as your user, with your credentials and caches.
- Lanes share Git metadata. With the Docker runner, they also share containers.

Use real sandboxing for anything you do not trust. Treat this tool as preventing accidents, not
attacks.

## Merge proof

Cleanup removes a lane only when each repository is clean and has one of these:

1. Its head is an ancestor of the base.
2. Its commits are patch-equivalent to the base, for histories without merge commits.
3. A forge reports a merged change at exactly this head, from the same repository.

A gone remote branch, a handoff and a quiet session prove nothing. Ignored files that are not listed
in `generated` also stop removal, because they may be someone's work. Removal never forces.

## Ownership

A lane is protected while a session lock is held, a process registered to its attachment is alive,
or its records are unreadable. Unknown means protected. Attachments carry a generation that changes on
each allocation, so a stale session cannot act on a lane that has moved on.

## Delegation

`bin/aw delegate` lets sessions in the owner checkout run maintenance for 5 minutes to 8 hours. Hooks
refuse agents' attempts to grant it or edit its record, and lanes never use it. It never covers
starting agents, deleting lanes, containers or applied sweeps.

## Platform notes

- **Linux** is the primary platform, and it is what this tool was developed on.
- **macOS** uses `ps` where Linux reads `/proc`. CI runs the test suite there, but it has not been
  used day to day, so treat it as less proven. Harness detection compares process names and may need
  adjusting for your harness on macOS. Claude Code does not rely on it, because it passes its PID.
- **Windows** is not supported. The tool relies on `fcntl` locks and POSIX process groups.
- Adapters depend on harness hook payloads, which are not stable interfaces. A harness update can
  break them, and when that happens the guard fails closed.
