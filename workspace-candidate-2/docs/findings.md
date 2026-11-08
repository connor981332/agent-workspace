# Findings from fabworkspace

What running several agent sessions on one machine taught the FabWorks workspace, taken from its
commit history, plan status files, and setup guide. Each finding names the contended resource, what
went wrong or nearly did, and where candidate 2 handles it, or doesn't. These matter more than any
script, because they are the requirements the final candidate has to meet.

## Containers and identity

1. **Two tools, one container.** VS Code and the CLI built rival containers until the Compose
   project name was pinned to the name VS Code derives (`<folder>_devcontainer`) and the CLI went
   through `devcontainer up`. *Candidate 2:* the core design (`bin/ws`, `project-template`).
2. **A checkout path is part of a container's identity.** A moved repo or a second clone reused the
   old container, which bind-mounted a missing path as an empty folder. The claim step removes such
   containers, and refuses to start when the other checkout is live and running. *Candidate 2:*
   `bin/devcontainer-init`, verified end to end by moving a workspace in `tests/smoke-docker.sh`.
3. **Containers don't gain ports or mounts when Compose files change.** The lane ports (3201–3204)
   only reached the app after its container was recreated, and adding docs mounts needed a recreation
   too. Agents must ask before recreating, because the owner may be attached. *Candidate 2:*
   `kit add-project` publishes every lane port from the first container, and the rules say to ask.
4. **Tooling drift breaks builds.** Docker Compose 5 builds through buildx bake, which refused the
   devcontainer CLI's generated Dockerfile in `/tmp`. Pinning the CLI version and setting
   `BUILDX_BAKE_ENTITLEMENTS_FS=0` fixed `bin/fw`. VS Code's rebuild still needs the variable.
   *Candidate 2:* same workaround, CLI pinned in `workspace.conf`.
5. **Agents created their own containers when allowed to.** The rules forbid `docker compose up` in
   project directories, `docker run`, and `devcontainer up` with other flags. Removing duplicates is
   the owner's call. *Candidate 2:* rules in `AGENTS.md`, and `bin/ws infra` refuses `down`.
   Enforcement is still cooperative.

## Shared infrastructure

6. **Two workspaces on one machine** (FabWorks next to RideTracker) needed offset host ports for every
   shared service. *Candidate 2:* `kit init --port-offset`, `{{PORT:n}}` in templates and modules.
7. **Start order must not matter.** Whichever project starts first brings up infrastructure. A warm
   start is a single `docker ps`, matched by service *name*, so a leftover container (MinIO after the
   Garage migration) can't stand in for a missing one. *Candidate 2:* `WS_INFRA_SERVICES`.

## Lanes

8. **Lanes came from verification, not from building.** A milestone's end-to-end checks and its owner
   checklist had to wait for the owner while the next milestone was built. A lane holds the finished
   milestone's state for the owner, so lanes live for *work*, not sessions. *Candidate 2:* lane
   lifecycle rule, and the `plans` module.
9. **Database names come from the project, so old branches are dangerous.** A branch from before
   lanes ignored `FW_LANE` and would have used the main checkout's databases. `bin/lane new` refused
   such branches, and `rm` drops databases by name, never through Rails. *Candidate 2:*
   `WS_LANE_GUARDS`; adapters drop by name.
10. **Secrets must be identical across lanes.** Sessions, signed IDs, and encrypted columns broke
    unless every worktree linked the main checkout's gitignored key files, and links had to be
    relative to resolve inside the container. *Candidate 2:* `WS_LANE_LINKS` and `ws_relpath`. The
    links also counted as untracked work and blocked `git worktree remove` in repos that don't ignore
    them. Candidate 2's tests found this, and the fix excludes them.
11. **Worktrees must live inside the bind mount.** `<repo>/.worktrees/<lane>` is visible in the
    container at `/<repo>/.worktrees/<lane>` with no new mounts. Git still runs on the host, because
    a worktree's `.git` names a host path. Workspace-repo worktrees appeared as siblings in `~/Code`
    until a rule put them in `.worktrees/`.
12. **Slots are scarce, so freeing them is a procedure.** Four slots meant lanes were removed only by
    their creator, only on the owner's word, after checking the worktree was clean, the branch merged
    or pushed, and the data saved in a snapshot. *Candidate 2:* `bin/lane rm` refuses unsaved work
    unless `--force`. Fabworkspace only documented the check.
13. **Restored snapshots lag the code.** A lane restored from an older snapshot needed migrations
    before any check, and migrating rewrote `db/schema.rb` in the worktree, which then had to be
    discarded. *Candidate 2:* documented; `lanes.d/setup` is the place to migrate. Not automated.
14. **Postgres can't copy a database in use.** Snapshots and restores need the lane's servers and
    consumers stopped. *Candidate 2:* the error says so; `WS_PG_TERMINATE=1` ends connections
    instead. That option is not in the original.

## What lanes don't isolate (open problems)

15. **Mail.** One Mailpit inbox for every lane. An acceptance check matched an earlier run's
    identical summary email and failed. Checks must count what they sent. *Open:* per-lane inboxes.
16. **Object storage.** One bucket. A snapshot restored into two lanes points both at the same files,
    so a lane-wide purge in one could delete files the other, or the snapshot, still uses. Handled by
    convention (purge single records in checks). *Open:* a `bucket` lane resource.
17. **Third-party sandboxes.** Every lane shares one Stripe sandbox and one Shippo test account. A
    restored lane reused record IDs another lane had already used in idempotency keys within Stripe's
    24-hour window. Stripe refused the request, or with matching parameters would have returned the
    other lane's object. The fix was in the app: put the record's creation time in the key. Webhook
    forwarders also receive every lane's events. *Open:* per-lane provider accounts, at the cost of
    onboarding each one.
18. **Human-gated setup.** Onboarding test accounts kept stopping for passwords, password-manager
    prompts, and identity checks. The answer was to do it once, keep the result as a snapshot
    (`m2onboarded`), and restore it before every run. Keys went into a gitignored file that tools
    read, so nothing prompts mid-session. *Candidate 2:* snapshots, `WS_LANE_LINKS`, and the
    `webhook-forward` and `plans` modules.

## Agents

19. **The owner's browser is not a test browser.** Driving it through an extension fought per-site
    zoom and window tiling. A headless, isolated Playwright MCP per session fixed screenshots and
    viewport sizes. *Candidate 2:* `browser-mcp`.
20. **Parallelism inside a session** came from an orchestrator that kept design and risky code, and
    handed briefed routine tasks to a cheaper implementer subagent that commits locally, never
    pushes, stops instead of guessing, and returns a fixed report. *Candidate 2:* `implementer-agent`.
21. **Agents must not read secrets.** Permission denies cover `.env` files and environment dumps, and
    scripts redact `whsec_` secrets from their output. *Candidate 2:* `.claude/settings.json`
    (also `.lanes/**`), and redaction in `webhook-forward`.
