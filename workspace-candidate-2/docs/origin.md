# From fabworkspace to candidate 2

Where every part of the FabWorks workspace went, and what was generic about it. The workspace is a
git repository at `~/Code/fabworkspace` with two project repos inside: `fabworks` (Rails 8) and
`fabworks-slicer` (a TypeScript worker running PrusaSlicer). They talk through Redis Streams and S3
objects. About 200 commits of product work ran through it, with several agent sessions working in
parallel through milestone M4.

## File by file

| fabworkspace | What it did | Candidate 2 | Classification |
|---|---|---|---|
| `bin/fw` | Run a command in a project container; start it with `devcontainer up`; `app@lane`; `status` | `core/bin/ws` | Generic. Renamed. Adds `infra` passthrough, per-project lane ports, and a clear error when `devcontainer up` doesn't produce a container. |
| `bin/devcontainer-init` | Claim step, network, warm-start infra check | `core/bin/devcontainer-init` | Generic, nearly verbatim. The service list and network come from `workspace.conf`, and `infra/compose.d/` is included. |
| `bin/lane` | Slots 1–4, app worktree, links to secrets, env file, Rails `db:prepare`, Postgres snapshots and drops, guard on `config/database.yml` | `core/bin/lane` plus `core/lib/ws/resources/{postgres,redis}.sh` | Generic mechanism, project-specific details. Postgres and Redis knowledge moved into adapters; Rails preparation moved to `lanes.d/setup`; the guard, links, and lane variable name became settings. |
| `bin/bootstrap` | Clone repos from the GitLab group | `core/bin/bootstrap` | Generic. The remote is a setting, and it warns when the checkout path differs from `WS_CHECKOUT`. |
| `bin/stripe-listen` | Stripe CLI forwarding to main or a lane; key from `.env.development.local`; secret to `.env.local`; redaction | `modules/webhook-forward` (`bin/webhook-forward`, `lanes.d/webhooks/stripe.sh`) | Provider-specific, generalized to a driver plus provider adapters. Half-built. |
| `projects` | `alias directory` lines | `template/projects` | Generic. Gains `worktree\|shared` and a port per project, which the original hard-coded in `bin/lane` (only the app had worktrees, port 320N). |
| `infra/compose.yaml` | Postgres, pgAdmin, Redis, Mailpit, Garage, CORS job, CoreDNS as `fw-infra` | `template/infra/compose.yaml` (Postgres, Redis) and modules `pgadmin`, `mail-catcher`, `object-store`, `dns-standin` | Split: the services lanes depend on stay in the template, and the rest became modules. Host-port offsets became `kit init --port-offset`. |
| `infra/garage.toml` | Single-node Garage config | `modules/object-store` | Module. |
| `infra/dns/` | CoreDNS fixture zone for custom domains | `modules/dns-standin` | Module, with a generic scenario zone. Half-built. |
| `AGENTS.md` | Product summary, project table, seven rules of engagement, docs authority | `template/AGENTS.md` | Rules generalized (Rails, GitLab, and slicer specifics removed). Lane lifecycle became its own rule, and a rule against reading secrets was added from `.claude/settings.json` practice. Modules append their rules. |
| `CLAUDE.md` | `@AGENTS.md` import | `template/CLAUDE.md` | Generic, verbatim. |
| `.claude/settings.json` | Deny reading `.env` files and dumping the environment | `template/.claude/settings.json` | Generic. Also denies `.lanes/**`. |
| `.claude/settings.local.json` | Enable the Playwright server | Not copied | Per-machine. |
| `.claude/agents/implementer.md` | Sonnet subagent: brief in, fixed report out | `modules/implementer-agent` | Generic pattern. FabWorks rules (tenancy, money in cents, multi-process boundary) removed. |
| `.mcp.json` | Playwright MCP, headless, isolated, fixed Chromium path | `modules/browser-mcp` | Module. The Chromium path was machine-specific and is dropped. |
| `.gitignore` | Project dirs, `.lanes/`, `.worktrees/`, Playwright output | `template/.gitignore` and module `gitignore` files | Generic. Kept in sync by `kit add-project`. |
| `docs/development/setup.md` | Container identity, ports, lanes, lifecycle, worktrees, troubleshooting, docs mounts | `template/docs/development/setup.md` | Generic text with placeholders. MinIO and Garage migration notes dropped. |
| `docs/README.md` | Docs map and authority rules | `template/docs/README.md` | The authority rules are generic. The map is project content. |
| `docs/contracts/slicing.md` | App ↔ slicer job contract | `template/docs/contracts/README.md` | The idea of a contract owned by the workspace, with a change order, is generic. The contract itself is product. |
| `docs/{product,architecture,design,operations}` | Product documentation | Not copied | Product. |
| `plans/README.md`, `plans/*/STATUS.md` | Plan index, statuses, verification practice, owner checklists | `modules/plans` | Practice generalized. The plans themselves are product history. |
| `<repo>/.devcontainer/{compose.yaml,devcontainer.json,on-initialize.sh,Dockerfile}` | Pinned Compose name, lane port range, docs mounts, agent config mounts, host-side init | `project-template/.devcontainer/` (rendered by `kit add-project`) | Generic shape. Toolchains, VS Code extensions, and the internal root CA became comments; agent config mounts are commented out so missing host dirs don't become root-owned folders. |
| `<repo>/config/database.yml` | `FW_LANE` suffix on database names | Documented in `postgres.sh`; `WS_LANE_VAR=FW_LANE` keeps the old name | The project-side half of a lane. It can't live in the workspace. |
| `.worktrees/` (workspace repo) | Workspace worktrees kept inside the root | Rule in `AGENTS.md` and setup docs; gitignored | Generic convention. |

## Mapping fabworkspace onto candidate 2

[examples/rails-and-worker](../examples/rails-and-worker/) is the original's shape with neutral names.
For FabWorks itself, the settings would be:

```sh
WS_NAME=fabworks  WS_PREFIX=fw  WS_NETWORK=fabworks-dev-net  WS_BASE_BRANCH=master
WS_LANE_VAR=FW_LANE
WS_LANE_GUARDS="app:config/database.yml:FW_LANE"
WS_LANE_LINKS="app:.env.development.local app:config/master.key app:tmp/local_secret.txt"
WS_PG_DATABASES="fabworks_development fabworks_development_cable fabworks_test"
WS_PG_SNAPSHOT_PREFIX=fabworks_snapshot_   WS_PG_CREATE=0
# projects:  app fabworks worktree 3200   /   slicer fabworks-slicer shared -
```

The mapping differs from the original in three places:

- **Snapshots** copy only the first database, as `bin/lane` did, so existing snapshots keep working.
- **The slicer in a lane:** `bin/fw slicer@lane` passed the lane's `PORT` (320N) to the slicer, which
  listens on 3100 and doesn't publish 320N. In candidate 2 a project with port `-` gets no `PORT`.
- **Redis on `rm --drop-data`:** candidate 2 flushes the lane's Redis database. The original left it.
