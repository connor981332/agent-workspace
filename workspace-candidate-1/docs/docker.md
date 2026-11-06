# Running lanes in containers

With `runner = "docker"`, commands run in one long-lived container per project, shared by every lane.
This keeps memory use flat as lanes grow, and stops host and container builds from sharing caches.

## The mount rule

A lane's `.git` file points into the owner checkout's `.git` directory by absolute path, so lanes must
appear at the same absolute path inside the container. Mount exactly these, and nothing else:

- the lanes directory
- the workspace `.git` directory
- each nested project's `.git` directory

The tool compares a running container's mounts with this list and refuses a mismatch. Never mount the
owner's working tree, because a lane could then reach it.

## Compose file

The tool sets `<NAMESPACE>_MAIN` and `<NAMESPACE>_LANES_DIR` when it calls Compose. With namespace `shop`:

```yaml
name: shop-agents

x-mounts: &mounts
  - {type: bind, source: "${SHOP_LANES_DIR:?}", target: "${SHOP_LANES_DIR:?}"}
  - {type: bind, source: "${SHOP_MAIN:?}/.git", target: "${SHOP_MAIN:?}/.git"}
  - {type: bind, source: "${SHOP_MAIN:?}/api/.git", target: "${SHOP_MAIN:?}/api/.git"}

services:
  agent-api:
    build: {context: "${SHOP_MAIN:?}/api", target: dev}
    command: /bin/sh -c "while sleep 1000; do :; done"
    init: true            # Reaps servers started detached.
    ports: ["4150:4150", "4250:4250"]
    volumes: *mounts
```

Give the container no `env_file`. A lane's environment is passed on every `docker exec`, so a command
run without it fails rather than reaching the wrong database.

The manifest then names the container:

```toml
[docker]
compose_file = "infra/agents.yaml"
project = "shop-agents"

[lanes]
published_slots = 2     # Must match the ports published above.

[projects.api]
runner = "docker"

[projects.api.docker]
container = "shop-agents-agent-api-1"
service = "agent-api"
user = "node"
```

The image needs `curl` for readiness checks, and `setsid` to start servers in their own process group.

## Managing containers

`bin/aw containers up|recreate|down` is owner-only. `recreate` and `down` refuse while any lane is
active or holds a slot. Containers start on demand when a lane needs them.
