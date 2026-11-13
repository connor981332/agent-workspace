# User experience

The CLI is part of the design, not an afterthought. Most of the tool's value is felt through it.

## Principles

- **`aw` alone is the dashboard.** It is the command people run most, so it is local-only and fast.
- **Names, not IDs.** Work is named from its purpose (`checkout-tax`). Commands accept unique
  prefixes and fuzzy matches (`aw resume tax`). Nobody types a GUID or a lane number.
- **Interactive only for people.** Pickers and prompts appear only on a terminal. Agents and scripts
  get deterministic output, `--json` and stable exit codes.
- **Every error names its fix.** `Lane checkout-tax: setup incomplete (db:prepare failed) → aw setup
  checkout-tax`.
- **Commands for agents and commands for humans are distinct.** Agents use `attach`, `run`, `serve`,
  `stop`, `finish` and team commands. Owner-only commands (`discard`, `pair`, `delegate`, container
  management, applied sweeps) are refused to agents by the guards.
- **Personal preferences live in the individual layer**: default harness, terminal multiplexer,
  `auto_sweep`, `owner_sessions`.

## Commands

| Command | Who | Does |
|---|---|---|
| `aw` | human | Dashboard: needs you, running, parked, cleanable |
| `aw new "<purpose>" [--harness h] [--mux] [--branch b]` | human | Claim a lane and launch a harness in it |
| `aw attach "<purpose>" [--branch b]` | agent | Claim a lane for the current session |
| `aw resume [name] [--all] [--mux]` | human | Reopen sessions where they left off |
| `aw open <name>` | human | Start a fresh session on existing work, with the handoff injected |
| `aw recap [--remote]` | human | Full status, for after a reboot or a weekend |
| `aw run <project> <cmd…>` | both | Run a command in the right place with the lane's environment |
| `aw <team-command>` | both | Run a named team command, queueing for its resources |
| `aw serve <resource>` / `aw stop <resource>` | both | Take or return a seat |
| `aw queue` | both | Who holds and who waits for each resource |
| `aw push [--as <name>] [project…]` | both | Push the lane's branch to origin, under the team's branch name if it differs, and remember that name for recap, finish and sweep |
| `aw finish --summary "…"` | agent | Hand off as ready for review |
| `aw park` / `aw unpark` / `aw discard` | human | Shelve, restore or abandon work |
| `aw sweep [--apply]` | human | Recycle lanes proven merged |
| `aw pull` | human | Fetch origin in every owner checkout, fast-forwarding those on the default branch, so new lanes start from the newest work |
| `aw pair` / `aw move-to-lane` / `aw try` | human | Work in the owner checkout ([owner-checkout.md](owner-checkout.md)) |
| `aw setup` / `aw doctor` / `aw inspect` / `aw rehearse` | human; an agent may rehearse its own onboarding lane | Onboarding and diagnosis |
| `aw snapshot <name>` / `aw restore <name>` / `aw snapshots` | both | Named copies of a lane's data, shared across the workspace |
| `aw setup <lane>` | both | Rerun a lane's setup from its first incomplete step; an agent may do this for its own lane |
| `aw config explain <key>` | both | Effective value and where it came from |
| `aw add module <name>…` / `aw modules` / `aw eject <module or file>` | human | Install, list and stop managing modules |
| `aw infra up\|ps\|logs` / `aw infra down` | both / human | The shared infrastructure |
| `aw webhook <provider>` | both | Send a provider's sandbox webhooks to this lane |
| `aw plan new\|list\|promote` / `aw check-docs` | both | Plans and their conventions |
| `aw inspect` / `aw doctor --projects` | both | Facts about the projects, and the deterministic readiness audit |
| `aw onboard [--review]` / `aw add project <alias> [url]` | human | Agent-proposed configuration in a lane; adding a repository |
| `aw delegate` | human | Expiring, scoped maintenance authority ([owner-checkout.md](owner-checkout.md#delegation)) |
| `aw changeset --title t` | both | Open this lane's linked merge requests in merge order; the publication policy applies |
| `aw upgrade` | human | Change the pinned framework version |

## Starting a session

Both paths are first-class, and neither needs a separate step to create a lane.

**`aw new "fix checkout tax rounding"`.** Claims a lane from the warm pool, names it
`checkout-tax`, records the purpose and launches the default harness in the lane root. The harness's
session ID is recorded from its first hook call. `--harness codex` overrides the default.

**`--branch <name>`** starts the lane on an existing branch, such as a teammate's branch or a change
set's, instead of a new one: the lane's worktrees check it out, tracking the remote branch when there
is one. The project's lane guards still run against it.

**Start the harness directly** (`claude`, `codex`) in the workspace, as today. The session starts in
the owner checkout, and reading, searching and planning work at once. **Lanes are claimed lazily, on
the first write.** The guard refuses the write with a message telling the agent to run
`aw attach "<purpose>"`, which claims a warm lane in milliseconds. The agent continues in the lane
root. A session that only reads never costs a lane.

The guard's message also tells the agent that the human can run `aw pair` instead, if they want the
agent in the owner checkout. An individual who always wants that can set `owner_sessions = "pair"`.

## The session record

For every session, `aw` records: harness, harness session ID, lane (or pair), purpose, start time,
last activity, state (`active`, `interrupted`, `ended`) and the latest handoff. The purpose comes from
`aw new` or `aw attach`; failing that, from the session's first prompt (captured by a hook and
truncated); failing that, from the branch name.

## Resuming

```
$ aw resume
  checkout-tax     claude   interrupted 2h ago   web, api dirty     "Fix checkout tax rounding"
  ios-onboarding   codex    ready for review                        "Onboarding flow v2"
  rails-upgrade    claude   parked 3 days                           "Upgrade to Rails 8.1"
```

Choosing one relaunches the right harness in the right lane with the harness's own resume flag and
the recorded session ID. `aw resume tax` skips the picker.

If the harness can no longer resume (the session expired, or it was on another machine), `aw` falls
back to `aw open`: a new session on the same lane, given the latest handoff and the plan's status, so
the agent resumes from the written record instead of from chat history.

## After a reboot

Lanes, state and Git all survive a reboot; processes do not. On the first `aw` command afterwards,
dead leases are recovered and sessions that were running are marked interrupted.

```
$ aw recap
NEEDS YOU
  ios-onboarding   ready for review   ios @ 3f2a91c, api @ 88be012          "Onboarding flow v2"
                   handoff: "Simulator tests pass. Verify Apple sign-in by hand."
  checkout-tax     interrupted        web: 2 dirty, 3 ahead    last 14:02   "Fix checkout tax rounding"
                   plan next step: "Add rounding tests for JPY"
RUNNING
  (none)
PARKED
  rails-upgrade    3 days
CLEANABLE
  2 lanes proven merged → aw sweep
```

`--remote` adds merge request and CI status from the forge. `aw resume --all --mux` reopens every
interrupted session, one per window or pane of the multiplexer chosen in the individual layer (herdr,
tmux or another; see [architecture.md](architecture.md#terminal-multiplexers)).

## Finishing and cleanup

See [lifecycle.md](lifecycle.md). In short: the agent runs `aw finish`, the lane stays protected
while the human verifies, the merge is proven, and `aw sweep` returns the lane to the warm pool.
Nothing unmerged is removed without an explicit `aw discard`.

## A day with the tool

1. Morning: `aw`. Two lanes need you, one session was interrupted, one lane can be cleaned up.
2. Verify `ios-onboarding` in its lane and approve the merge request. After it merges, the lane is
   recycled.
3. `aw resume tax` puts the checkout fix back in front of Claude.
4. `aw new "add webhook retries" --harness codex` claims a second lane at once.
5. An agent needs the Rails stack and queues for it. You need it too: `aw serve rails` in your
   checkout evicts the agent and boots your copy. When you stop, the agent gets it back.
