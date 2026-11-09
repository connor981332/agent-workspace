# Safety model

Agent Workspace keeps several coding agents from getting in each other's way, or yours. This page
says what it protects you from, and what it does not.

## The short version

**The guards are for cooperative agents. They are not a security sandbox.** They stop an agent that
is trying to do its job from making a mistake: deleting your branch, editing your checkout, killing
your dev server, printing your secrets. They do not stop an agent that is trying to get around them.

On the host runner, agents run as your user account and can do anything your account can do, unless
something else prevents it. Do not run untrusted agents, or agents reading untrusted prompts, on the
strength of this tool. For that, use real isolation: a container, a virtual machine or a separate
account.

## What the guards do

Before a supported harness runs a tool call, it asks `aw`. The call is allowed, refused with a message
that says what to do instead, or passed to you for approval.

| Protected | How |
|---|---|
| Your checkout | A lane agent is confined to its lane. File tools and literal paths in shell commands that reach outside it are refused. An agent that has not claimed a lane may read, but not change anything. |
| Your branches and tags | Git's `reference-transaction` hook refuses ref updates from a lane outside that lane's own branches. It runs inside Git, so `--no-verify` does not skip it. |
| Shared Git configuration | `git config` writes, `-c core.hooksPath=…` and `git worktree` commands are refused. |
| Your processes | `kill`, `pkill` and the like are refused. Configured servers are started through `aw serve`. |
| Containers | Commands that create, restart or remove containers are refused. |
| Secrets | Reading or echoing a project's secret files, the environment, or a process's environment is refused. |
| Who may do what | A team role can remove publishing or merging from a lane, such as the implementer role. Pre-authorizing narrow publication and delegating routine maintenance are time-limited or reviewed configuration. |
| Your decisions | Owner-only commands, such as `aw discard` and `aw pair`, refuse agents. Pushing and opening merge requests ask you first. |

**Guards fail closed.** If `aw` cannot decide, the call is refused. That covers an invalid
configuration, a state database it cannot read, a payload it does not recognize, a command it cannot
parse, a path it cannot compute and a missing binary.

**Pair sessions** are agents you have invited into your own checkout with `aw pair`. They may edit
there. They are still refused destructive Git, and they still need your approval to publish or to
change the workspace's own instructions, hooks and configuration.

## What the guards do not do

- **They read commands, not intent.** A shell command is parsed, and the literal paths and known
  dangerous commands in it are checked. A program that writes files by its own logic, such as a
  Python script, is not visible to the guards. Nor is what a test suite or a build does when it runs.
- **They cannot see what they are not told about.** A harness without hooks is not confined: it gets
  the instructions and `bin/aw`, and nothing enforces them. Claude Code, Codex, Grok and OpenCode have
  hooks. Check that `aw setup` has run and that its files are committed, or a lane will not have them.
- **They depend on the harness.** A guard runs only if the harness calls it. If a harness update
  changes its hook format, `aw` refuses what it does not recognize. If a harness stops calling its
  hooks, or fails to load OpenCode's plugin, nothing is checked.
- **Secrets can still reach an agent.** Tools started through `aw run` get the project's environment.
  An agent can run a program that reads a secret and reports it, and the guards cannot see that.
- **The network is not restricted.** An agent can reach whatever your machine can reach.
- **Your other files are not protected.** The boundary is the workspace. A lane agent's access to
  paths elsewhere on your machine, such as your home directory, is limited only by your account.

## What this means in practice

- Review an agent's work before you merge it. Lanes make that easier, and they do not replace it.
- Keep credentials with broad access out of reach of the account that runs agents.
- Treat text an agent reads from the outside world, such as issues, web pages and dependencies, as
  able to change what the agent does.
- Run `aw doctor` if something looks wrong. It reports hooks that have been changed or are missing.
