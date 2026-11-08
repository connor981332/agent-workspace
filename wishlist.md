# Agent Workspace Wishlist

1. **Three layers: framework, team, individual.** The team layer adds repos, commands, instructions and rules. The personal layer can override the team, except for settings the team locks. You can see where any setting came from.
2. **Clone-and-go onboarding.** One setup command after cloning the team repo.
3. **Controlled framework updates.** The team pins a framework version, updates it deliberately, sees what changed, and can detect local edits.
4. **Multi-repo isolated workspaces per agent.** One task can span several repos, each with its own setup steps, and cleanup never loses work.
5. **Coordinating single-instance resources.** Exclusive or capacity-limited resources, a fair queue, time limits, recovery when a holder crashes, human priority (the engineer can take the resource and agents yield), and notifications to agents.
6. **Dev-container execution.** Paths mean the same thing inside and outside the container, the container is never started or stopped unexpectedly, and disruptive operations are refused while others are using it.
7. **Named team commands.** Test, lint, migrate and so on, each run in the right place with the right resource held.
8. **Composed agent instructions and skills.** Built from all three layers, with hand-edits detected, and without dirtying the team repo.
9. **Team-defined guardrails and hooks.** Teams can add their own hooks, which declare their requirements.
10. **Human review for self-modification.** A person approves changes to the workspace's own instructions, hooks and config.
11. **Private personal data stays private.** Personal plans, notes and skills are kept out of the team repo.
