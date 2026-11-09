---
name: aw-readiness-audit
description: Find what each project must change to work well in lanes, and propose the changes as a change set.
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
3. Fix each on this lane's branch, one finding at a time, with a test where one is practical.
4. Open the fixes with `bin/aw changeset`: the same branch in each affected project, linked merge requests and a merge order. The publication policy applies.
5. Propose a lane guard for the most important fixes, so that branches from before the fix are refused.
6. Stop, and report each finding and what you did about it. A person reviews the result.
