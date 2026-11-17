# Agent Workspace

`aw` lets one developer run several coding agents at once, across many repositories, without them
stepping on each other or on the developer. Read this before changing anything. Details live in
`docs/spec/`; this file says what wins when they pull in different directions.

## North star

**The developer stays in charge and never waits on the tool.** Agents get as much room as safety
allows, and the developer can always step in, take back control, or say "I mean it."

## Principles, in priority order

When two of these conflict, the one higher on the list wins.

1. **Never lose work.** aw destroys only work it has proven merged, and it archives even that.
   When it can't tell, the answer is "unproven," never a guess.
2. **Never strand anyone.** Every state aw puts a person or session into has one obvious exit, a
   single command. A refusal names the command that gets you past it, and the human can always
   override with one command, never by having to start a new session.
3. **The human is the authority.** The owner checkout belongs to the human. Agents propose; the
   human approves. aw serves the developer and doesn't second-guess them.
4. **Regulate only what aw creates.** Default guards cover lanes, seats and aw's own state. Every
   other guard (Git, publishing, secrets, processes) is opt-in. Guard against harm, not location:
   being in a particular directory is not, on its own, a reason to refuse.
5. **The tool is never the bottleneck.** Guard decisions take milliseconds, read-only commands
   never touch the network, and slow work runs in the background with progress shown. Only the
   user's own project should ever make someone wait.
6. **Fix it, don't report it.** If a safe fix exists, apply it, or print the exact command. Fail
   once, early, and say why, rather than failing three times with the same wall of text. Report
   only what needs a decision.
7. **Ceremony in proportion to risk.** The safe route has to cost less than the risk it removes;
   otherwise people and agents will route around it.
8. **Honest about safety.** Hooks are guardrails for cooperative agents, not a sandbox. Say so, and
   fail closed.

## How a protection should behave

| Tier | Use it for | What aw does |
|---|---|---|
| Silent | Reads; edits inside your own lane | Nothing |
| Warn | Reversible, local, but unusual | Proceed, and say why it matters |
| Redirect | A mistake with an obvious fix | Do the right thing, or print the exact command |
| Guard | Irreversible, outward-facing, or someone else's work | Stop, give the reason, offer a one-command human override |

A guard's message says what it protects ("could destroy unmerged work"), not where you are ("not
in lane").

## Before shipping a feature, ask

- Could it leave someone with no way out?
- If it refuses, what does it protect, and is that worth a round trip to the human?
- Does it add steps or latency between a person and their work? If so, what justifies them?
- Does every message it prints tell the reader what to do next, in one line?
- Can an agent read its output (plain text, `--json`, stable exit codes) as easily as a human?

## Working in this repository

- Acceptance scenarios build aw at runtime: run them with `-count=1` and
  `TMPDIR=~/.cache/aw-tmp`.
- Plans live in `plans/` and follow the status convention in `plans/README.md`. Numbered
  requirements are in `docs/spec/requirements.md`; cite them by number.
