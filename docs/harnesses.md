# Supported harnesses

`aw` guards a harness through the harness's own hook system. `aw setup` writes the configuration, and
a guard that cannot run refuses the call rather than allowing it.

| Harness | Guarded through | Personal instructions | Notes |
|---|---|---|---|
| Claude Code | `.claude/settings.json` hooks | `CLAUDE.local.md` | |
| Codex | `.codex/hooks.json` | session-start context | Asks once to trust a project's hooks. |
| Grok | Claude Code's hook file | session-start context | Asks once to trust the folder (`/hooks-trust`). Notifications from the queue are not shown yet. |
| OpenCode | A plugin, `.opencode/plugins/aw.js` | not yet | `aw new` and `aw pair` are not tied to the session they launch. Notifications from the queue are not shown yet. |

A harness without hooks still gets the composed `AGENTS.md`, but nothing guards its calls.

## Adding another harness

A team can add a hooked harness without a new release of `aw`, with a mapping from its payload fields and tool names in `[harnesses.<name>]` of `workspace.toml`, and point its hooks at `bin/aw hook <name> <event>`. A tool the mapping does not list is refused. The [configuration reference](reference/config.md) lists the keys.

When a harness changes its hook format, its adapter refuses calls until a release of `aw` supports the
new format, and the error says so.

## Harness sandboxes

Codex and Claude Code have their own sandboxes. `aw` never turns one on, because that is your choice.
It writes what a sandbox needs if you use one, local to each checkout and kept out of Git:

- `.codex/config.toml`: `sandbox_workspace_write.writable_roots`.
- `.claude/settings.local.json`: `sandbox.filesystem.allowWrite`.

Both list the repositories' Git directories, which hold `aw`'s state, and in a lane its worktrees. If
your team commits its own `.codex/config.toml`, `aw` leaves it alone and says what to add.

Codex reads a project's configuration only once you trust the project.
