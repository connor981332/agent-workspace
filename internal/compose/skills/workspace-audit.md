---
name: aw-workspace-audit
description: Audit this workspace's existing configuration with the developer, against the repositories as they are and what this aw can do now, and make only the changes they agree on.
---

# Workspace audit

Agents propose, the tool verifies, a human approves. This is a conversation with the developer about a configuration that already exists. You change nothing until they agree, and then only what they agreed. Do not commit to the default branch.

This audits the workspace's configuration. What the projects themselves must change to work in lanes is the readiness audit's (`aw-readiness-audit`, `bin/aw doctor --projects`). Mention it when it matters, and do not do its work here.

1. Gather the facts, and reason from them instead of rediscovering them:
   - `bin/aw inspect --json`: the projects as they are, `audited_with`, aw's `suggestions`, and `changelog_since`, what changed in aw since the configuration was last designed or audited, with the configuration keys each item names;
   - `bin/aw config validate` and `bin/aw doctor`;
   - `workspace.toml`, the hook scripts, and `docs/workspace-design.md`, which records earlier decisions and their reasons;
   - `bin/aw config explain <key>` for any key you are about to discuss.
2. Brief the developer. Keep it short, and rank each group by what a change would gain: lanes that start, set up or serve faster first, then fidelity and safety, then tidiness. For each item say what it gains, what it costs, and what you recommend.
   - *The repositories changed*: a new project, server, dependency on data, or gitignored file a lane needs.
   - *aw changed*: aw's suggestions, and the changelog items since `audited_with` that touch keys or features this workspace uses or could use. Leave out items that do not apply here.
   - *Worth discussing again*: a recorded decision whose premise a newer aw changed. Name the changelog item that changed it. Do not reopen a recorded decision without such a reason, and do not raise what `[audit] declined` lists.
   If there is nothing worth changing, say so plainly. That is a good result.
3. Discuss. Wait for the developer, and follow their lead: answer questions, compare the alternatives with what each costs in speed, memory and fidelity, and change your recommendation when they give you a reason. A project change is an option with its tradeoffs, never a requirement. Edit nothing while you discuss.
4. Confirm. List what was agreed and what was declined, and ask once whether to go ahead.
5. Apply only what was agreed, in `workspace.toml`, the hook scripts and the harness files on this lane's branch. For each suggestion they declined, add its id under `[audit] declined` with their reason. Record any other idea they declined in `docs/workspace-design.md`, with the reason, so the next audit does not raise it again.
6. Verify with `bin/aw config validate` and `bin/aw doctor`. When lanes, servers, hooks or devcontainers changed, run exactly `bin/aw rehearse`, from the lane root and with no `cd` prefix or pipe, so that the permission aw granted matches it. When it refuses a project's own file, treat it as the design skill does: `accepted_findings` with the reason if the configuration never starts that file, an option for the developer if it is a real problem in lanes, a request for aw if aw should handle it.
7. Record the audit: set `[workspace] audited_with` to the `version` that `bin/aw version --json` reports, and add a dated entry under "Audits" in `docs/workspace-design.md` saying what was considered, what was chosen and what was declined. If nothing changed, ask before recording: recording is itself a change that goes through review, and if the developer says no, write nothing.
8. Summarize what changed and why. The result goes on this lane's branch and through a merge request, because hooks run with the user's privileges and a person must review them. In a pair session in the owner checkout there is no branch: leave the changes there for the person to review. Then stay in the conversation: the developer may want to go on.
