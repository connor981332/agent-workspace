---
name: aw-readiness-audit
description: Find what keeps each project from working well in lanes, sort each finding into what aw should handle, what the project could change, and what to accept, and offer the project changes as options.
---

# Readiness audit

Agents propose, the tool verifies, a human approves. You are writing a proposal.

1. Run `bin/aw doctor --projects`, which reports what a program can find, and `bin/aw inspect --json`.
2. Then look for what a program cannot find:
   - ports that cannot be set from the environment;
   - database names that do not vary per lane;
   - fixed Redis databases, queue names, bucket names or file storage paths;
   - browser cookies scoped to `localhost`, which collide across lanes (use `AW_HOSTNAME`);
   - idempotency keys for external providers that can repeat across restored snapshots;
   - secrets read in ways that break through links, or printed in logs;
   - absolute paths that differ between host and container.
3. Sort every finding into one of three groups, and say which and why:
   - **aw should handle it.** A gap or a bug in aw, which the project should not work around. It becomes a request for aw, in your report, not a project change.
   - **A real lane problem in the project.** Something that would go wrong in lanes even with a perfect aw, such as a gitignore missing a rule for local env files, so per-lane env files show as untracked. Only these become project changes.
   - **Accept it.** Reviewed and fine, such as a Compose file the configuration never starts. Record it in the project's `accepted_findings`, with the reason.

     ```toml
     [projects.web.accepted_findings]
     "compose.legacy.yaml" = "the configuration never starts it; lanes use the shared database"
     "ci/**" = "CI has its own services"
     ```

     The key is a path relative to the project (`dir/**` matches a directory), and the value says why it is fine.
4. Present the project changes to the human as options, each with its tradeoff, and make only those the human chooses, on this lane's branch, one finding at a time, with a test where one is practical.
5. Open the chosen fixes with `bin/aw changeset`: the same branch in each affected project, linked merge requests and a merge order. The publication policy applies.
6. Propose a lane guard for the most important fixes, so that branches from before the fix are refused.
7. Stop, and report each finding, its group and what you did about it. A person reviews the result.
