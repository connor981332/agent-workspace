# Sessions and lanes

How a session gets its identity, how it changes, when a session holds a lane, and what state a lane
is in. This is the design for Phase 5 of [plans/dogfood-0.3.md](../../plans/dogfood-0.3.md). Once
it is built, it replaces the parts of [owner-checkout.md](owner-checkout.md) and
[lifecycle.md](lifecycle.md) that it names. It applies principles 2 ("Never strand anyone") and 7
("Ceremony in proportion to risk, and to who is present") in [AGENTS.md](../../AGENTS.md).

## What is wrong today

Today a session's identity comes from two sources that disagree. With an attachment record, the
lane wins wherever the session runs, and nothing but process death or recycling the lane ends it:
`aw finish` changes the lane, not the session, and `aw pair --session` refuses a session attached
to a lane. Without a record, the working directory decides on every tool call
(`internal/harness/hook.go`, `resolveScope`), so a shell that drifts into a lane's worktree makes
the session that lane's agent, and a pair session stops being one. The result is identity that
moves when it should stay still, and a binding that stays when it should end.

## Two separate things: scope and hold

A session record now keeps two things apart:

- **Scope** is what the guards let the session do: *unattached* (the owner checkout, read-only for
  aw's own rules), *pair* (acts for the human), or *lane N* (writes in lane N, at generation G).
- **Hold** is whether the session is using the lane. A lane is *held* while a session whose scope
  is that lane has not released it and its process is alive. Sweep keeps a held lane; nothing else
  about the session protects it.

Scope decides permissions; hold decides whether the work is in progress. Releasing a lane ends the
hold and, for a session that came to the lane from elsewhere, also returns its scope.

## Scope is set when a session starts

A session's scope is decided once, from:

1. **The command that launched it.** `aw new` and `aw resume` give lane scope, `aw pair` gives pair
   scope, through `AW_SESSION`, as now.
2. **Otherwise, the directory its harness started in.** A plain `claude` started in a lane root has
   that lane's scope and holds it. Started in the owner checkout it is unattached, or pair with
   `owner_sessions = "pair"`.

The scope is recorded at `SessionStart`. A harness that sends no `SessionStart` gets its record at
its first tool call, from that call's directory, the nearest thing to where it started. After that,
**the working directory never changes the scope.** A session in the wrong place meets the guard
tiers instead (Phase 6): a read gets a note, a write meant for its own lane is redirected with the
path it meant, and a write into another lane or the owner checkout is guarded as before.

## Scope changes only through commands

The scope changes like a stack one deep. The session's *home* is the scope it started with.

| Command | From | To | Hold |
|---|---|---|---|
| `aw attach "<purpose>"` | unattached | lane | held |
| `aw attach "<purpose>"` | its own lane | the same lane (idempotent) | held again |
| `aw attach "<purpose>"` | pair | refused, with `aw move-to-lane "<purpose>"` instead; the session stays paired | none |
| `aw attach "<purpose>"` | another lane | refused: `aw detach` first | unchanged |
| `aw detach` | lane | its home: unattached, or the lane itself if it started there | released |
| `aw finish --summary` | lane | as `aw detach`, after writing the handoff | released |
| `aw pair --session <h:id>` (the human) | anything | pair. If the lane it leaves has uncommitted work, it warns, since the work stays in the lane | released |
| session end, process death | any | — | released |

`detach` cannot widen anything: it only returns a session to a scope it already had, and only the
human grants pair. A session whose home is a lane stays in that lane's scope after it releases it.
If it goes on working there, it holds nothing, and the lane's derived state shows that its
heads have moved since the handoff (below).

An unattached session that writes in the owner checkout after it released a lane is refused as
today. The refusal names its previous lane: `aw attach "<that lane's purpose>"` takes it back. It is
not taken back automatically.

`aw move-to-lane` from a pair session creates the lane and moves the work as now, and the session
stays paired. A pair session may already write in lanes, so it can commit and push there without
being attached.

## Lane state is derived

The stored lane states shrink to what only a person or a step can say: *claiming*, *setting up*,
*incomplete* (a setup step failed), *parked* (the human's intent) and *pool*. A claimed lane that is
none of these has a derived state, worked out when it is shown or swept:

| Derived state | When |
|---|---|
| **working** | A live session holds it. |
| **ready for review** | Nobody holds it, it has a handoff, and every repository's head is the one the handoff recorded. |
| **changed since its handoff** | Nobody holds it, and a head moved after the handoff, or a worktree has uncommitted changes. |
| **interrupted** | Nobody holds it, it has no current handoff, and the last session to hold it ended without finishing: its process died, or it was closed. |
| **idle** | Nobody holds it and it has no handoff, and no session ever held it, such as a lane made with `aw attach` from a script. |
| **recyclable** | Every repository is clean and proven merged (unchanged rule), whatever the above. |

`aw`, `recap` and sweep show these. `aw lanes` reads no repository, to stay fast, so it shows
*working*, *ready*, *interrupted* and *idle*, and leaves *changed* and *recyclable* to the others. "Interrupted" and "changed since its handoff" show
in `aw`'s *needs you* section, with `aw open <lane>` to have a fresh agent write a handoff. JSON keeps
the `state` field, with these names; `working` and `ready` keep their meanings.

## Finishing

`aw finish --summary "…"` is now two things: the **handoff** (heads, the summary, the plan link,
what to verify), which only the agent can write, and the **release** (the hold ends, leases return,
servers stop), which also happens on detach, session end, process death and the human's re-pair.
`finish` no longer stores *ready*; *ready for review* follows from the handoff.

## Lazy per-project setup

Claiming a lane creates the worktrees of the projects it starts with (see "Lazy worktrees" below),
their links and copies, and runs the workspace-level steps (`prepare`, data providers) and each of
those projects' `prepare`, which writes the env files commands read. A project's other steps
(install, its lifecycle hooks, its checkout hook) run the first time the lane needs that project:
`aw run`, `aw serve` or a team command in it. These already wait for the steps they need
(`lifecycle.Needed`); they now start them if nothing has. `aw setup <lane>` runs everything, as
today, and `--project <name>` one project. The guard's hook cannot start a setup, since it must
stay fast and read-only, so a first *edit* in a project does not set it up; the first command that
runs something there does. A command an agent runs directly in a project not set up yet is let
through with a note naming `bin/aw run <project>` and `bin/aw setup <lane> --project <name>` (the
warn tier).

## Lazy worktrees

From plans/dogfood-0.4.md (decisions 3 to 6). In a workspace of fifteen projects most lanes touch
two or three, and a worktree costs time at claim and at recycle, disk, and noise in `aw lanes` and
`aw sweep`. So a lane checks a project out on first use, as it already sets one up on first use.

- **What a claim creates.** The projects in `[lanes] eager`, those named with `aw new --projects`
  or `aw attach --projects`, and everything they `need`, recursively. `[lanes] setup = "eager"`
  makes every project eager, for both checkout and setup, as before. A single-repository workspace
  (project `directory = "."`) always has its one project: the lane root is its worktree. A warm
  lane from the pool keeps the worktrees it already has, and the claim puts each of them on the new
  lane's branch too: switching an existing worktree is cheap, and a worktree in the lane that is not
  on its branch would be work aw cannot see.
- **`bin/aw use <project>…`** adds projects to the caller's own lane: their worktrees on the lane's
  branch, everything they need, their secret links, local files and link from the lane root, then
  each one's `prepare`. Its other setup steps wait for first use, unless the project is eager. It is
  idempotent, any agent may run it in its own lane without being asked, and it shows progress.
  `aw run` and `aw serve` call it first for a project the lane does not have yet.
- **What an agent sees first.** The lane root has links only to the projects it has. The generated
  instructions list the workspace's projects and `bin/aw use`. A file tool or a shell command that
  names a path under `<lane>/<project>` for a project the lane does not have is refused with "web
  isn't checked out in lane 03 → run bin/aw use web": the redirect tier, since the agent fixes it
  with one command and the human isn't asked. The hook decides this from the lane's recorded
  repositories and a `Lstat`, so it stays read-only and within its budget. Without the redirect, a
  write there would create a plain directory in the workspace repository's lane worktree.
- **Dependencies.** `[projects.<name>] needs = ["<project>", …]` names the projects whose worktrees a
  project's build or tests read. Using a project checks out what it needs first. Validation refuses
  an unknown name and a cycle.
- **Workspace-level steps.** The workspace's `prepare` and data providers run at claim time, before
  most projects are checked out, so they may not assume that any project's directory exists in the
  lane. The audit and design skills check for this; a team whose `prepare` needs a project lists it
  in `eager`.
- **Recycling and the pool.** Sweep, merge proof, discard and recycle already act on the lane's
  recorded repositories, so a lane with some projects is proven, recycled and removed like any
  other. A recycled lane keeps its worktrees, clean at the base, as a warm lane does today.
- **Claude Code.** The owner checkout's local settings list each project's `.worktrees` directory
  (architecture.md), and they reload while a session runs, so a worktree added by `aw use` is
  reachable without a prompt or a restart.

## Capacity

When a claim finds every lane in use, it first recycles the lanes that nobody holds and that are
proven merged, exactly as `aw sweep --apply` would, with the same archiving, and then claims. Only
if none can be recycled does it fail, naming what is kept and why. This is safe by construction:
the lanes it recycles are the ones the human's own sweep would.

## Routes and precedence

The generated instructions state the precedence: what the human says in the conversation, then the
team's instructions, then aw's. They describe two routes: an agent working alone claims a lane and
hands off; a pair session works in the owner checkout, the human reviews the diff, and
`aw move-to-lane` gives a branch when one is wanted. `aw audit` and `aw onboard` start pair
sessions by default, and `--lane` starts one in a lane.

## State migration

State schema 14:

- `sessions` gains `home` (`unattached`, `pair` or `lane`) and `released` (a time, or empty). A
  record with a lane and no `released` holds it as long as its process is alive. The hook records a
  session that starts in a lane as holding it only when it can find the session's process;
  otherwise the session has the lane's scope, and `aw attach` makes it hold the lane.
- Lanes stored as `working` or `ready` become `claimed`. Their handoffs are already stored.
- Existing records do not say where a session started. A session with a lane gets home
  `unattached`, as if it came from the owner checkout, so that `detach` returns it there; one that
  started in the lane can attach again from there.

## Requirements that change

- **R23** (lanes belong to work) gains: a session holds a lane only while it is alive and has not
  released it, and every session has a one-command exit from a lane.
- **R60, R61** (pair): the human's `aw pair --session` always succeeds, and a pair session does not
  attach.
- **R78** (finish): finish writes the handoff and releases; it no longer stores *ready*.
- **R80** (interrupted sessions): an interrupted lane is a derived state.
- **R21** keeps worktrees in every project; their setup becomes lazy, which R31 and R32 still cover.
