# macOS devcontainer checklist

GitHub's macOS runners have no Docker, so this is run by hand on a Mac, on **Docker Desktop** and on
**OrbStack**, at M6 and before each release (risk 4). Record the date, the engine and its version,
the macOS version and any failure in the release's pull request.

Use a workspace with one devcontainer project and a server, kept under a path reached through a
symlink if you can (for example `/tmp`, which is `/private/tmp`).

| # | Do | Expect |
|---|---|---|
| 1 | `aw doctor` | The project's devcontainer and the CLI's pin are reported as fine. |
| 2 | `aw containers up` | The container starts, labelled with this checkout. |
| 3 | `aw run <project> pwd` | The same path as on the host. No mount refusal: Docker Desktop's reported mount path is understood. |
| 4 | Open the project in VS Code, "Reopen in Container" | VS Code attaches to the same container; `docker ps` still shows one. |
| 5 | With VS Code attached, `aw containers recreate <project>` | Refused, listing the VS Code processes. |
| 6 | Close VS Code. In a lane, `aw serve <resource>` | The URL it prints answers from the host's browser. |
| 7 | `aw whoami`, then `kill -9` the `aw __keep` process, then `aw stop <resource>` | The server is gone from `docker exec <id> ps`. |
| 8 | In a lane, `aw run <project> sh -c 'sleep 100'`, then Ctrl-C | The `sleep` is gone from the container. |
| 9 | Run `aw run <project> cat` with input piped in | Input arrives, and the exit status comes back. |
| 10 | Stop the container, move the workspace, `aw run <project> pwd` from the new path | The old container is removed, and the new one mounts the new path. |
