---
name: aw-workspace-design
description: Design this workspace's lanes, resources, providers and lifecycle hooks from what aw inspect finds, then prove the result with aw rehearse.
---

# Workspace design

Agents propose, the tool verifies, a human approves. You are writing a proposal. Do not commit to the default branch.

1. Run `bin/aw inspect --json`, and reason from its facts instead of rediscovering them.
2. For each project, decide and explain:
   - whether lanes get a worktree, or run the owner's checkout with lane settings;
   - which servers exist, which are scarce, and how they group into resources, with capacity, preemption and time limits;
   - which data needs a provider, and what template it starts from;
   - which lifecycle hooks are needed, and write them, safe to rerun;
   - which team commands exist, and what each needs;
   - which token resources exist, such as emulators or simulators;
   - which gitignored files a lane needs, from what aw inspect lists: `secrets` for anything an agent must not read, `local_files` for the rest, and nothing that is not needed;
   - which settings the project's hooks read, in `[projects.<name>.env]`.
3. Write `workspace.toml`, the hook scripts, and `docs/workspace-design.md`, which records each decision and the reason for it.
4. Check it with `bin/aw config validate` and `bin/aw doctor`.
5. Run exactly `bin/aw rehearse`, from the lane root and with no `cd` prefix or pipe, so that the permission `aw onboard` granted matches it, and work from what it reports. It tests the lane as it is now, in a throwaway copy with its own containers, so the human's checkout is not touched. Iterate until it passes.
6. Stop, and report what you decided and why. The result goes on this lane's branch and through a merge request, because hooks run with the user's privileges and a person must review them. In a pair session in the owner checkout there is no branch: leave the changes there for the person to review.
