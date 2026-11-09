# Getting started

Agent Workspace gives each coding agent its own lane: a worktree of every repository, its own data,
and a place in line for the things only one lane can use at a time. You keep your own checkout.

It runs on Linux and macOS, on amd64 and arm64.

## Join a workspace that already uses it

You need `sh`, `git`, `curl` or `wget`, and `sha256sum` or `shasum`. Nothing else.

```sh
git clone git@github.com:shop/shop-workspace && cd shop-workspace
bin/aw setup
```

`bin/aw setup` downloads and verifies the version the workspace pins, clones the project repositories,
creates your individual settings, checks for secret files that are not in Git, installs the hooks,
installs dependencies, and runs `aw doctor`. You can run it again at any time. It asks before it
starts any container.

Then start work:

```sh
aw new "add feature flag"
```

That claims a lane and launches your harness in it. Or start a harness yourself in the workspace, and
it will attach to a lane when it first needs to write.

## Set up a new workspace

Install a global `aw`. Inside a workspace it always runs that workspace's pinned version, so upgrading
it changes nothing there.

```sh
brew install connor981332/tap/aw
# or
curl -fsSL https://raw.githubusercontent.com/connor981332/agent-workspace/master/install.sh | sh
# or
go install github.com/connor981332/agent-workspace/cmd/aw@latest
```

In the repository that will hold the workspace:

```sh
aw init
```

Review `workspace.toml`, then commit what `aw init` wrote, and run `aw setup`. The
[configuration reference](reference/config.md) lists every key, and `aw config explain <key>` says
where a value comes from.

To have an agent propose the configuration, run `aw onboard`. It works in a lane, and its result stays
on that branch until you review it. Before you merge it, run `aw rehearse`, which creates two lanes at
once and reports anything that collides. `aw add project <alias> <url>` adds a repository later.

## Every day

| Command | What it does |
|---|---|
| `aw` | Shows the lanes, the queue and what needs you. |
| `aw new "<purpose>"` | Claims a lane and launches a harness in it. |
| `aw run <project> <command>` | Runs a command in the project, in this lane. |
| `aw serve <resource>`, `aw stop <resource>` | Takes and gives up a seat on a resource, starting and stopping its servers. |
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
