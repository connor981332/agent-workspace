# The owner checkout

The default checkout of the workspace repository, and of every project repository inside it,
belongs to the human. The human should be able to work there with plain Git, their editor and their
own dev server, as if no agents were present. They never have to run `aw` in it.

The owner checkout's path is recorded in state. Owner-only commands are those run from it by the
human, outside any agent session, or by a session holding a delegation. A command is in an agent
session when a harness's environment marker is set (`CLAUDE_CODE_SESSION_ID`, `CODEX_THREAD_ID`,
`GROK_SESSION_ID`), when `AW_SESSION` is set (`aw new`, `aw resume` and `aw pair` set it), or when a
known harness is among its ancestor processes. So an owner-only command refuses an agent even for a
harness without hooks, or a command the guards did not see. The ancestry is read only for owner-only
commands, off the hot path.

## What protects it

| Could be disturbed | Protection |
|---|---|
| **Files** | Lane agents are confined to their lane. File tools and literal paths in shell commands that reach the owner checkout are refused. Undeterminable paths fail closed. |
| **Branches and refs** | Worktrees share the repository's `.git`. Git already refuses to check out a branch that another worktree has checked out. `aw` adds a `reference-transaction` hook, installed through its hooks dispatcher, that refuses any ref update made from a lane outside that lane's own branches: deleting or moving the owner's branches, rewriting the stash, deleting tags. The hook runs inside Git, so it catches what the shell parser cannot see, and `--no-verify` does not skip it. The guards also refuse `-c core.hooksPath=…`, `git config` writes to shared configuration and `git worktree` commands outside `aw`. |
| **Index, dependencies, build output** | Each worktree has its own index and its own dependency and build directories. |
| **Dev servers and ports** | The owner uses seat 0 and the base ports. Lanes use offset ports. Agents cannot run `kill`, `pkill` or similar. Eviction only stops lane processes. The owner's servers count against capacity and always win the queue. |
| **Databases** | Providers never touch the owner's databases or a template. Lane creation refuses a branch whose configuration would reach the owner's databases. |
| **Secrets** | Lanes get read-only links to the owner's gitignored secret files. Guards refuse writing through those links and reading or echoing them. |
| **Shared containers** | Agents cannot recreate, restart or stop containers. Disruptive operations are refused while the owner is attached, or anything else is using the container ([architecture.md](architecture.md#runners)). |
| **Machine load** | Lane limits and resource capacity keep agents from starving the owner's work. |

On the host runner, agents run as the human's user account. These protections stop accidents by
cooperative agents. They do not stop an agent determined to get around them.

### What a lane may change

The `reference-transaction` hook decides at `prepared`, and only for a lane. **Allowed:** the lane's
own branches, refs private to a worktree (`HEAD`, `ORIG_HEAD`, `FETCH_HEAD`, `REBASE_HEAD`,
`AUTO_MERGE` and the like, and `refs/bisect/*`), `refs/remotes/*`, and updates that leave a ref's
resolved value unchanged, as `git pack-refs` and `git gc` do. **Refused:** other branches, tags,
`refs/notes/*`, `refs/aw/*` and `refs/stash`, which every worktree shares, so a lane commits to its
own branch instead. `aw`'s own updates from a lane carry a short-lived token recorded in state. A
push into the repository (`git push . a:b`) runs inside its Git directory and cannot be traced to a
checkout, so it is refused from anywhere; update such a branch from the owner checkout with `git
merge` or `git branch`.

### The Git hooks dispatcher

Teams often have their own Git hooks (`core.hooksPath`, for example `.githooks/`). `aw setup` points
every repository's `core.hooksPath` at a small dispatcher that runs `aw`'s own checks first and then
the team's hook of the same name. In the owner checkout the dispatcher runs the team's hook without
starting `aw`, so plain Git there keeps working even if `aw` cannot run. `aw doctor` reports a
repository whose hooks path has been changed.

## Pair sessions

Sometimes the human wants an agent in their own checkout, working alongside them. That is a **pair
session**.

- **Starting one.** `aw pair` launches the default harness in the owner checkout with the pair role.
  For a session already running in the owner checkout, the human runs `aw pair --session
  <harness>:<id>`. A harness alone knows its session's ID, so the refusal an unattached session
  receives spells out that command for it. Only the human can grant pairing: as with delegations, hooks refuse an agent that tries to
  grant pairing or edit its record. `owner_sessions = "pair"` in the individual layer makes every
  directly started session in the owner checkout a pair session. `aw new` still gives lanes.
- **Writes** in the owner checkout are allowed. No lane is claimed.
- **Resources.** A pair session acts for the owner: its `aw serve` uses seat 0 and preempts agents.
- **Still refused:** destructive Git (force pushes, deleting branches, `reset --hard` over
  uncommitted changes), outward-facing forge actions beyond the publication policy, and changes to the
  workspace's own instructions, hooks, guards and configuration, which still need review. Pairing
  grants the checkout, not ownership of the workspace. This covers file tools, shell redirects and
  the common file commands. Writing inside a Git directory is refused, not asked, because `aw`'s
  record of the session is there.
- **Several pair sessions** are allowed, with a warning, because two agents editing one tree is the
  collision lanes exist to prevent.
- Pair sessions appear in `aw`, `recap` and `resume`. `aw finish` writes a handoff but protects
  nothing, because the owner checkout is never cleaned up.

## Delegation

`aw delegate --scope maintenance --ttl 1h` lets sessions in the owner checkout do routine maintenance
for 5 minutes to 8 hours: `aw setup`, `park`, `unpark`, `sweep --apply`, `containers up`, and the
commands listed in `[delegation] maintenance`. It never covers starting agents, discarding work,
pairing, delegating, upgrading, or recreating or removing containers. Lanes never use it. Only the
human can grant it. A record that has expired, names another scope or claims more than 8 hours counts
as none. (Decided by the spec owner, 2026-11-09.)

## Roles

A lane can work under a team role (`aw new --role`, `aw attach --role`):

```toml
[roles.infra]
allow = ["containers"]              # publish, merge, containers, servers
deny = []
instructions = "docs/infra-role.md" # a session receives it when it starts
```

The built-in `implementer` denies `publish` and `merge`. No role can override path confinement,
secrets, destructive Git or owner-only commands. An agent may take on a role that only removes
abilities; a role with `allow` is the human's to assign. (Decided by the spec owner, 2026-11-09.)

## Moving work between the owner checkout and lanes

- **`aw move-to-lane "<purpose>"`.** Commits the owner checkout's uncommitted work to a new branch in
  each affected repository, restores the owner checkout to its branch, claims a lane on the new
  branch and moves the session there. Used when a pairing task should finish on its own.
- **`aw try <lane>`.** Checks out the lane's exact heads, detached, in the owner checkout so the human
  can run the work in their own environment. Refuses when the owner checkout has uncommitted changes.
  `aw try --back` returns each repository to the branch it was on.
