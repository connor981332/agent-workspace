# Agent Workspace Wishlist

> The maintained, numbered requirements are now in [docs/spec/requirements.md](docs/spec/requirements.md). This list is kept as their origin.

Agent Workspace has three levels of specialization, and every item below should be read in that light:

- **Framework** (generic): lives in this repository and knows nothing about any particular product.
- **Team / organization**: a product's workspace. It adds repos, commands, rules and shared infrastructure.
- **Individual**: one developer's overrides, plans, notes and skills.

1. **Three layers: framework, team, individual.** The team layer adds repos, commands, instructions and rules. The personal layer can override the team, except for settings the team locks. You can see where any setting came from.
2. **Clone-and-go onboarding.** One setup command after cloning the team repo.
3. **Controlled framework updates.** The team pins a framework version, updates it deliberately, sees what changed, and can detect local edits. Updating must not strand existing lanes (see item 20).
4. **Multi-repo isolated workspaces per agent.** One task can span several repos, each with its own setup steps. Cleanup never loses work: it removes only work proven merged, never work that merely looks merged.
5. **Coordinating single-instance resources.** Exclusive or capacity-limited resources, a fair queue, time limits, recovery when a holder crashes, human priority (the engineer can take the resource and agents yield, including the engineer's own dev servers, which count against the limit), and notifications to agents.
6. **Dev-container execution.** Paths mean the same thing inside and outside the container, the container is never started or stopped unexpectedly, and disruptive operations are refused while others are using it.
7. **Named team commands.** Test, lint, migrate and so on, each run in the right place with the right resource held. A command declares whether it needs a server slot: unit tests do not, browser and end-to-end runs queue for one.
8. **Composed agent instructions and skills.** Built from all three layers, with hand-edits detected, and without dirtying the team repo.
9. **Team-defined guardrails and hooks.** Teams can add their own hooks, which declare their requirements.
10. **Human review for self-modification.** A person approves changes to the workspace's own instructions, hooks and config. An expiring, scoped owner delegation (for example, routine maintenance for one hour) is the controlled alternative to blanket approval.
11. **Private personal data stays private.** Personal plans, notes and skills are kept out of the team repo.
12. **Cross-repo change sets.** A change that spans repos is a first-class thing: the same branch name in each repo, merge requests linked to each other with a declared merge order, and contract changes made in order (producer before consumer). Generated contracts are never hand-edited.
13. **Forge integration.** Not tied to one forge (GitLab with `glab`, GitHub with `gh`). Agents can open merge requests, check CI and merge. Publication policy is configurable by the team: the default is to ask before anything outward-facing, and a team can pre-authorize narrow cases, such as workspace-only changes after CI passes.
14. **Per-agent provisioned data.** Each agent can get its own database, cache or storage bucket, cloned from a protected template that nothing may connect to. Provisioning, teardown, reset, and snapshot/restore are supported (for example, keeping onboarded provider test accounts as a snapshot).
15. **Shared dev infrastructure as a managed thing.** Shared services (databases, caches, object storage, mail catcher, admin tools) are optional add-on modules. Agents never take the stack down, and each agent is handed its own ports and URLs.
16. **External-service stand-ins.** Webhook forwarding to the right agent's server, a mail catcher, a DNS stand-in, and sandbox provider accounts for live acceptance tests.
17. **Secrets handling.** Dev secrets live in gitignored files that agents' workspaces link to. Tools read them so agents never need to print them, and guards stop agents from reading or echoing them.
18. **Browser and end-to-end tooling per agent.** Teams declare MCP servers and browser tooling at the team layer. Each agent gets an isolated browser, separate from the engineer's.
19. **Handoff and session lifecycle.** The full path from "work done" to "ready for review" to "safe to delete": a handoff records exact heads, releases shared resources, and keeps the workspace protected. Also: parking and unparking, reconciling after a crashed or abandoned session, resuming by explicit session ID, and a status view showing what needs the human, what is running and what can be cleaned up.
20. **State versioning and migration.** Workspace state carries a schema version. Framework updates migrate it, and state with an unknown schema stays protected rather than being deleted.
21. **Plans and docs, owned by the right level.** The framework provides the mechanism (templates, a status convention a reader with no session memory can resume from, a plan index, a docs checker) but ships no content.
    - **Individual** is the default home for plans, notes and scratch docs. They stay out of the team repo (item 11).
    - **Team** holds the docs and plans that are genuinely shared: product definition, contracts between repos, cross-project plans, bugs and todos the whole team works from.
    - A plan or doc can be **promoted** from individual to team deliberately, and the team's conventions apply once it is there.
    - Teams can opt out entirely and bring their own system.
22. **Multi-harness parity.** Works with any agentic coding tool (Claude Code, Codex, Grok and others). One canonical instruction file, with each tool's file importing it. Guard behavior is the same across harnesses, and a harness check verifies it.
23. **Roles with different permissions.** The team layer can define roles, such as owner, implementer (never pushes or merges) and infrastructure agent, each with its own permissions and instructions.
24. **Idempotent bootstrap and adding repos later.** Setup is safe to rerun, and there is a documented path to add a project repo to an existing team workspace.
25. **Doctor and self-diagnosis.** Every failure names its fix. A read-only diagnostic mode works before setup has completed.
26. **Resource budgeting.** Limits on concurrent agents and servers, idle expiry, and an eviction grace period, because dev servers cost real memory.

## Non-functional

- **An honest safety model.** Hooks are guardrails for cooperative agents, not a sandbox, and the documentation says so. Guards fail closed. Real sandboxing is an optional stretch goal.
- **Testable without Docker.** The framework's own tests run against stubbed container tooling.
- **Platform support.** Linux and macOS, both covered by real tests.
