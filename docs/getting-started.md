# Getting started

Agent Workspace gives each coding agent its own lane: a worktree of every repository, its own data,
and a place in line for the things only one lane can use at a time. You keep your own checkout.

It runs on Linux and macOS, on amd64 and arm64.

## Install

You need `sh`, `git`, `curl` or `wget`, and `sha256sum` or `shasum`. Nothing else.

```sh
curl -fsSL https://raw.githubusercontent.com/connor981332/agent-workspace/master/install.sh | sh
aw version
```

This puts `aw` in `~/.local/bin`, after verifying its checksum, and its signature when `cosign` is
installed. `AW_VERSION` picks a release and `AW_INSTALL_DIR` the directory. Inside a workspace, `aw`
always runs the version that workspace pins, so upgrading this copy changes nothing there.

## Join a workspace that already uses it

```sh
git clone git@github.com:shop/shop-workspace && cd shop-workspace
bin/aw setup
```

`bin/aw setup` downloads and verifies the version the workspace pins, clones the project repositories,
creates your individual settings, checks for secret files that are not in Git, installs the hooks,
installs dependencies, and runs `aw doctor`. You can run it again at any time. It asks before it
starts any container. You do not need the global `aw` for this.

Then start work:

```sh
aw new "add feature flag"
```

That claims a lane and launches your harness in it. Or start a harness yourself in the workspace, and
it will attach to a lane when it first needs to write.

## Set up a new workspace

In the directory that will hold the workspace, which can be a Git repository or a plain directory of
project repositories:

```sh
aw init
aw onboard
```

`aw onboard` is the one command to start with. It runs everything `aw setup` does, reports what
lanes will need, and starts an agent to design the configuration. Nothing has to be committed first.
If setup or creating a lane fails, the agent starts anyway, with the failure in front of it, to fix
it, skip it or ask you.

The agent's result stays on a lane's branch until you review it. A workspace kept as plain files has
no branch to review, so there the agent works in the owner checkout as a pair session. Before you
merge, run `aw rehearse`, which creates two lanes at once and reports anything that collides.
`aw add project <alias> <url>` adds a repository later.

`aw setup` is the part of onboarding that involves no agent. Use it alone on a fresh clone of a
workspace that is already configured, and `aw setup <lane>` to retry a lane's setup.

Hooks see your environment, so `GENERATE_API=NO aw onboard` reaches them. To keep a setting, put it in
`workspace.toml` under `[projects.<name>.env]`. The [configuration reference](reference/config.md)
lists every key, and `aw config explain <key>` says where a value comes from.

### Files Git does not have

A new worktree has only what Git tracks, and `aw inspect` lists the gitignored files that would be
missing. Lanes get only what a project declares:

```toml
[projects.web]
secrets = [".env"]                     # linked read-only, and agents may not read them
local_files = ["schema.graphqls"]      # copied into each lane, as a clone where the disk can
```

## Every day

| Command | What it does |
|---|---|
| `aw` | Shows the lanes, the queue and what needs you. |
| `aw new "<purpose>"` | Claims a lane, named from the purpose, and launches a harness in it. The lane starts from origin's default branch as last fetched. `--shell` gives you your own shell there instead. |
| `aw pull` | Fetches origin in every checkout and fast-forwards those on the default branch, so new lanes start from the newest work. |
| `aw lanes` | Lists the lanes. `aw recap` shows each in full. |
| `aw run <project> <command>` | Runs a command in the project, in this lane. |
| `aw serve <resource>`, `aw stop <resource>` | Takes and gives up a seat on a resource, starting and stopping its servers. |
| `aw push [--as <name>]` | Pushes the lane's branch, under your team's branch name if it differs from `aw/<lane>`. |
| `aw finish` | Hands a lane off for review. |
| `aw sweep`, `aw sweep --apply` | Shows, then does, the cleanup of lanes proven merged. |
| `aw doctor` | Checks the workspace and this machine, and names the fix for each problem. |

Agents may not run the commands that change your work or the shared machine, such as `aw discard`,
`aw pair` and `aw containers`. Read the [safety model](safety.md) before relying on any of this: the
guards are for cooperative agents, and are not a security sandbox.

## Verify a release yourself

Every release carries `checksums.txt`, a Sigstore bundle for it, and build provenance. With `cosign`
installed, the launcher and `install.sh` check the signature for you. By hand:

```sh
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/connor981332/agent-workspace/\.github/workflows/release\.yml@refs/tags/v' \
  checksums.txt
gh attestation verify aw_<version>_<os>_<arch> --repo connor981332/agent-workspace
```
