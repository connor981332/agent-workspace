# Safety model

Agent Workspace keeps several coding agents from getting in each other's way, or yours. This page
says what it protects you from, and what it does not.

## The short version

**The guards are for cooperative agents. They are not a security sandbox.** They stop an agent that
is trying to do its job from stepping outside its lane: editing your checkout, or moving another
lane's branch. With the optional safeguards on, they also stop it deleting history, killing your dev
server or printing your secrets. They do not stop an agent that is trying to get around them.

On the host runner, agents run as your user account and can do anything your account can do, unless
something else prevents it. Do not run untrusted agents, or agents reading untrusted prompts, on the
strength of this tool. For that, use real isolation: a container, a virtual machine or a separate
account.

## What the guards do

Before a supported harness runs a tool call, it asks `aw`. The call is allowed, refused with a message
that says what to do instead, or passed to you for approval.

| Protected | How |
|---|---|
| Your checkout | A lane agent changes files only in its lane: file tools and shell commands that would change your checkout or another lane are refused. It may read anywhere, and is told when what it read is your copy of a file its lane has too. An agent that has not claimed a lane may read, but not change anything. |
| Your branches and tags | Git's `reference-transaction` hook refuses ref updates from a lane outside that lane's own branches. It runs inside Git, so `--no-verify` does not skip it. |
| Shared Git configuration | In a lane, `git config` writes go to you, and `-c core.hooksPath=…` is refused. `git worktree` commands are refused everywhere, because `aw` manages worktrees. |
| Your servers | Configured servers are started through `aw serve`, so that they take a seat. |
| Who may do what | A team role can remove publishing or merging from a lane, such as the implementer role. Pre-authorizing narrow publication and delegating routine maintenance are time-limited or reviewed configuration. |
| Your decisions | Owner-only commands, such as `aw discard` and `aw pair`, go to you for approval. |

## Optional safeguards

`aw` does not assume how you want agents to use Git, your forge or your machine, so onboarding changes
none of that. Your harness's permissions and your forge's branch protection still apply. To have `aw`
guard more, turn on the groups you want in `workspace.toml`:

```toml
[safeguards]
enable = ["git-safety", "publication", "secrets", "processes", "containers"]
```

| Group | What it does |
|---|---|
| `git-safety` | Asks you before force pushes, deleting remote or local branches, and destructive commands such as `git reset --hard`. |
| `publication` | Asks before pushing, forge writes and publishing packages or images. `[publication] allow` pre-authorizes a lane's own push, merge request and merge. |
| `secrets` | Asks you before an agent reads or changes a project's secret files, and refuses printing the environment, naming the one variable to print instead. |
| `processes` | Asks you before `kill`, `pkill` and the like. |
| `containers` | Asks you before commands that create, restart or remove containers. |

**Guards fail closed where a rule depends on the answer.** That covers an invalid configuration, a
state database it cannot read, a payload it does not recognize and a missing binary. A command or path
`aw` cannot read is refused in a lane or when a safeguard or team guard is on, unless the command
only reads (with the `secrets` safeguard on, even then). In your own checkout, an unattached session
is asked instead.

**An invalid configuration still leaves the way back out.** While the configuration cannot be
read, every session may still run `aw doctor`, `aw config validate` and the Git commands that only
read what Git tracks (`git status`, `git log`, `git diff`, `git show`). A pair session may also
switch branch (without forcing) and run `aw upgrade`. Everything else is refused, with a message
saying what still works.

**Pair sessions** are agents you have invited into your own checkout with `aw pair`. aw's own rules
do not apply to them: they may edit there and in lanes, change the workspace's own instructions,
hooks and configuration, and run your `aw` commands, such as `aw upgrade` or `aw discard`. Your
team's guards and the safeguards you turned on still apply, and so do your harness's own permission
prompts.

## Tiers

Each protection is in the tier of whoever has to act (AGENTS.md, "How a protection should behave").
A harness that cannot ask (Codex and OpenCode today) refuses what would be asked, and says so.

| Tier | Who acts | The guards' rules in it |
|---|---|---|
| Silent | nobody | Reads anywhere; edits inside the session's own lane; a pair session's work |
| Warn | nobody | A lane session reading from the owner checkout, through a relative path or a command run there: it goes through, with a note naming the lane's copy. A command run directly in a project the lane has not set up yet (lazy setup). |
| Redirect | the agent | A lane writing outside itself, or from the owner checkout by a relative path or command; an unattached session writing in the owner checkout (`aw attach`); a new or renamed branch in a lane; `git worktree`; starting a configured server directly (`aw serve`); printing the environment; a pre-authorized push or merge that is not the lane's own; a team role's limits; a team guard set to deny |
| Guard | the human | `git-safety`'s force pushes, deleted branches and discarded work; `git config` and `git remote` changes; stopping processes; changing shared containers; reading or changing a secret file; publishing beyond `[publication] allow`; the human's own commands (`aw discard`, `aw sweep --apply`, …); a command an unattached session runs that aw cannot classify; a team guard set to ask |
| Hard stop | nobody | Turning the guards off (`git -c core.hooksPath=…`, `core.fsmonitor`, `core.sshcommand`, aliases); running a command in a multiplexer pane outside the lane; a call aw could not check: an unreadable payload, a command, path, directory or option whose value aw cannot determine, a team guard's missing file, and anything but the way back out while the configuration is invalid |

While a guard's question is open, `aw` shows the lane under *needs you* as waiting on you, until the
session's next tool call.

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

## What the guards cannot see

Code an interpreter runs (`python3 -c`, `node -e`) and HTTP clients such as `curl` are not read, so what they do is not guarded. A harness without hooks is not confined at all: it gets the instructions and `bin/aw`, and nothing stops it. [docs/dev/security-review.md](dev/security-review.md) lists what has been reviewed.
