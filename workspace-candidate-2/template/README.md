# {{TITLE}} Workspace

<!-- What the product is, in a sentence. -->

This workspace owns product and technical documentation, shared development infrastructure, and tooling for running several coding agents at once. Application code lives in separate repositories cloned inside it and excluded from its Git history.

| Repo | What it is |
|---|---|
<!-- One row per project. -->

## Quick start

Requires Docker, Node (for the devcontainer CLI), and access to the repos. Use this checkout path every time, because dev containers are identified by their host path:

```sh
git clone <this repo> {{CHECKOUT}}
cd {{CHECKOUT}}
bin/bootstrap
bin/ws <alias> --up
```

See the [shared development setup](docs/development/setup.md) for containers, ports, lanes, and troubleshooting. Run project code inside its dev container.

## Contents

| Path | Purpose |
|---|---|
| [docs/](docs/README.md) | Product, architecture, contracts, development |
| [AGENTS.md](AGENTS.md) | Shared working rules; `CLAUDE.md` imports it |
| `projects` | Project aliases, directories, lane modes, and ports |
| `workspace.conf` | Names, network, lanes, and lane resources |
| `infra/` | Shared dev services |
| `lanes.d/` | Lane setup and teardown hooks, and workspace lane resources |
| `bin/ws`, `bin/lane` | Run commands in containers; parallel lanes |

Git commands at this root affect the workspace only. Use `git -C <repo>` for project changes.
