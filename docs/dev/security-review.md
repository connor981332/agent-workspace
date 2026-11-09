# Security review of the guards and the launcher

Done at M12 by reading the code and sending about seventy attempted bypasses through the real hook. It is the implementer's own pass, so an independent review is still wanted before 1.0.

The guards stop mistakes by cooperative agents. They are not a sandbox ([safety.md](../safety.md)), and this review holds them to that.

## Found and fixed

| Finding | Fix | Scenario |
|---|---|---|
| An option's value before a subcommand was read as the subcommand, so `docker compose -p x down`, `docker --context x stop` and `gh --repo a/b pr merge` passed. | The guard knows which options take a value. | `policy/options-before-subcommand` |
| A command whose name is computed, such as `$(echo aw) discard one`, was allowed. | Refused. | `policy/fails-closed-on-computed` |
| A file-changing command given a value known only at run time, such as `rm -rf "$OLDPWD"`, was allowed in a lane. | Refused. | `policy/fails-closed-on-computed` |
| A script fed to a shell on standard input, such as `curl … \| sh`, was allowed. | Refused. | `policy/fails-closed-on-computed` |
| With `cosign` installed, a failed download of a release's signature was treated as an unsigned release, and verification was skipped. | Only a real 404 counts as unsigned. | `launcher/verifies-signature` |

## Accepted, and documented

- Code an interpreter runs (`python3 -c`, `node -e`) is not read, so its effects are not guarded. Reading it would need a sandbox.
- HTTP clients, such as `curl -X POST` to a forge's API, are not recognized as publication.
- An agent runs as the user, so it can write to `~/.cache/agent-workspace` and replace a cached binary. The launcher verifies a binary only when it downloads it, to stay off the hot path.
- `AW_TOOL_PATH` runs another binary. It is for developing `aw`, and a harness's hooks do not inherit a value an agent sets in a shell command.
- The Git hooks dispatcher does not run `aw` in the owner checkout, on purpose: plain Git must work there even if `aw` cannot.

## What was checked and held

Paths through `cd`, `pushd`, symlinks and `git -C` / `--git-dir`; `env`, `command`, `nohup`, `sudo` wrappers; force pushes and ref deletions; `git config` and `-c core.hooksPath`; process killing; secret files and environment dumps; `aw` reached by another path; agents granting pairing or delegation; a tampered delegation record; the launcher's checksum, exact-version and `.aw/lock` parsing.
