# Agent Workspace

`aw` lets one developer run several coding agents at once, across many repositories, without them
stepping on each other or on the developer. The developer's team shares the workspace's
configuration. At run time, the developer at the keyboard decides. Read this before changing
anything. Details live in `docs/spec/`; this file says what wins when they pull in different
directions.

## North star

**Many agents, one developer in charge: nobody waits on the tool, nobody gets stuck, and no work
is lost.**

How well aw works depends on how well a workspace is configured, so onboarding and audit are part
of the product, not an add-on.

## Principles, in priority order

When two of these conflict, the one higher on the list wins.
Where the code doesn't follow a principle yet, new work follows the principle; `plans/` says
what is in progress.

1. **Never lose work.** aw destroys only work it has proven merged, and it archives the tip unless
   the history itself is in the base. When it can't tell, the answer is "unproven," never a
   guess.
2. **Never strand anyone.** Every state aw puts a person or session into has one obvious exit, a
   single command. A session's identity changes only through aw's commands, never because of the
   directory it happens to be in. Recovery commands always work, even when the configuration or
   state is broken.
3. **The human is the authority.** The owner checkout belongs to the human. Agents propose; the
   human approves. When instructions disagree, the human in the conversation wins, then the
   team's instructions, then aw's.
4. **Restrict only what aw creates; inform about anything.** Default guards cover lanes, seats and
   aw's own state. Every other guard (Git, publishing, secrets, processes) is opt-in. Checks,
   diagnoses and suggestions may cover anything that affects the work, such as prerequisites, the
   forge or project files. Guard against harm, not location.
5. **The tool is never the bottleneck.** Guard decisions take milliseconds, read-only commands
   never touch the network, and slow work runs in the background or on first use, with progress
   shown. Only the user's own project should ever make someone wait.
6. **Fix it, don't report it.** If a safe fix exists, apply it, or print the exact command. Fail
   once, early, and say why. Report only what needs a decision, and say plainly what is waiting on
   the human.
7. **Ceremony in proportion to risk, and to who is present.** An agent working alone gets a lane.
   When the human is present, their review is the safeguard, and aw asks for nothing more.
   Self-modification always needs a human's review, but that review doesn't have to be a merge
   request.
8. **Honest about safety.** Hooks are guardrails for cooperative agents, not a sandbox. Say so.
   Fail closed on writes, never on the way back out.

## How a protection should behave

Choose the tier by who has to act.

| Tier | Who acts | Use it for | What aw does |
|---|---|---|---|
| Silent | nobody | Reads; edits inside your own lane | Nothing |
| Warn | nobody | Reversible, local, but unusual | Proceed, and say why it matters |
| Redirect | the agent | A mistake with an obvious fix | Refuse, naming the exact command that works |
| Guard | the human | Irreversible, outward-facing, or someone else's work | Ask in the harness, so the human approves in place |
| Hard stop | nobody | Turning off the guards themselves; a call aw could not check | Refuse |

A message says what it protects ("lane 03's uncommitted work"), not where you are ("not in
lane").

## Two audiences

People and agents both read aw's output. On a terminal, a person gets structure, colour and live
progress, ending with what needs them. Anything else, including every agent session, gets plain,
stable text, `--json` and exit codes. Give each audience its own material: don't put a list meant
for an agent in front of the person, or the person's prose in front of the agent.

## Before shipping a feature, ask

- Could it leave someone with no way out?
- If it refuses, what does it protect, and is that worth a round trip to the human?
- Does it add steps or latency between a person and their work? If so, what justifies them?
- Does every message it prints tell the reader what to do next, in one line?
- Would a person and an agent each find what they need in its output?

## Working in this repository

- Acceptance scenarios build aw at runtime: run them with `-count=1` and
  `TMPDIR=~/.cache/aw-tmp`.
- Plans live in `plans/` and follow the status convention in `plans/README.md`. Numbered
  requirements are in `docs/spec/requirements.md`; cite them by number.
