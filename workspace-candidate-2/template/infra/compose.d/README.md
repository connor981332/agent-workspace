# Infrastructure modules

Each `*.yaml` here is merged after `../compose.yaml`, in name order, by `bin/devcontainer-init` and
`bin/ws infra`. Relative paths inside these files resolve against `infra/`, so a module keeps its
files in `infra/<module>/` and mounts them as `./<module>/...`. Add a long-running service's name to
`WS_INFRA_SERVICES` in `workspace.conf`; leave one-shot jobs out.
