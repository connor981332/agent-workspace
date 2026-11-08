# implementer-agent

> A Claude Code subagent on a cheaper model that implements one briefed task, commits locally, and reports back for review.

**Status:** working pattern; the brief format is informal. **Origin:** fabworkspace
`.claude/agents/implementer.md`.

The orchestrating session plans, writes a brief (repo, branch, the docs to read, acceptance checks,
the attribution line), and keeps design decisions and risky code for itself. The implementer follows
the workspace rules, runs code only through `bin/ws`, never pushes, merges, or touches containers,
stops instead of guessing, and ends with a fixed report so review is mechanical.

This is how fabworkspace parallelized *inside* one session; lanes parallelize *across* sessions. An
implementer works in the orchestrator's lane and branch. Edit the project-specific rules in the file
(tests first for which areas, which checks verify a change) after installing.
