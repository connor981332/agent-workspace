---
name: aw-workspace-design
description: Design this workspace's lanes, resources, providers and lifecycle hooks from what aw inspect finds, then prove the result with aw rehearse.
---

# Workspace design

Agents propose, the tool verifies, a human approves. You are writing a proposal. Do not commit to the default branch.

aw adapts to the projects as they are. A project change is an option you offer, with its tradeoffs, never a requirement you impose.

1. Run `bin/aw inspect --json`, and reason from its facts instead of rediscovering them.
2. Find the alternatives the projects already offer, and stop to ask the human about each before you write any configuration. Existing tooling says what the team prefers, so read it as a statement, not as noise. Look for:
   - a devcontainer beside commands that also run on the host. If it lists a gitignored local Compose
     slot, offer `projects.<name>.local_compose` so aw can own that whole slot (mount, seat ports and
     label); keep the editor's workspaceFolder. With no slot or committed aw wiring, aw generates
     `.devcontainer/aw/devcontainer.json` and the developer selects its `(aw)` choice in VS Code;
     Rebuild keeps that choice. Offer a local slot as a small optional project MR if the developer
     prefers one configuration: list it and create it when missing in initializeCommand without
     overwriting it. Explain the picker versus single-configuration tradeoff; ask before
     changing the project, and do not replace a local file whose settings the developer needs;
   - an in-memory or mock switch beside the real service in Compose, such as an event bus that can run without Kafka;
   - a database in Compose and one on the host;
   - Compose profiles, or a gitignored settings file, through which each developer already opts into services;
   - earlier worktree tooling, and how it behaved (a single app slot, a shared container).
   For each, give the human the choices, what each costs in speed, memory and fidelity, and your recommendation, then wait for the answer. Record the question, the choice and the reason in `docs/workspace-design.md`. Where the team supports several setups, the configuration supports each of them. Decide silently only where there is one reasonable answer.
3. For each project, decide and explain:
   - whether lanes get a worktree, or run the owner's checkout with lane settings;
   - which servers exist, which are scarce, and how they group into resources, with capacity, preemption and time limits;
   - which data needs a provider, and what template it starts from;
   - which lifecycle hooks are needed, and write them, safe to rerun;
   - which team commands exist, and what each needs;
   - which token resources exist, such as emulators or simulators;
   - which gitignored files a lane needs, from what aw inspect lists: `secrets` for anything an agent must not read, `local_files` for the rest, and nothing that is not needed;
   - which settings the project's hooks read, in `[projects.<name>.env]`.
4. Write `workspace.toml`, the hook scripts, and `docs/workspace-design.md`, which records each decision and the reason for it. Set `[workspace] audited_with` to the `version` that `bin/aw version --json` reports, so that a later audit knows which aw this design was made with.
5. Check it with `bin/aw config validate` and `bin/aw doctor`.
6. Run exactly `bin/aw rehearse`, from the lane root and with no `cd` prefix or pipe, so that the permission `aw onboard` granted matches it, and work from what it reports. It tests the lane as it is now, in a throwaway copy with its own containers, so the human's checkout is not touched. When it refuses a project's own file, do not edit the project to make it pass: if the configuration never starts that file, record it in `accepted_findings` with the reason; if it is a real problem in lanes, offer the project change to the human as an option; if aw should handle it, say so in your report as a request for aw.
7. Stop, and report what you decided and why. The result goes on this lane's branch and through a merge request, because hooks run with the user's privileges and a person must review them. In a pair session in the owner checkout there is no branch: leave the changes there for the person to review.
