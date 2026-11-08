# Distribution, onboarding and upgrades

## Artifacts

- Static Go binaries for Linux and macOS, amd64 and arm64, built with GoReleaser.
- Published as GitHub Releases with a SHA-256 checksum file, Sigstore (cosign) signatures and SLSA
  provenance.
- Semantic versioning. The framework version, the configuration schema version and the state schema
  version are separate numbers, and each release states all three.
- First-party modules are part of the release, so one version covers the binary and its modules.

Ways to get a first `aw` onto a machine, for running `aw init`:

- Homebrew tap.
- `curl -fsSL https://…/install.sh | sh`, which verifies the checksum.
- `go install`.
- Later: mise and aqua registries, a Nix flake.

There is no pip or npm package.

## The global `aw` is a proxy

A globally installed `aw` is a bootstrapper. Inside a workspace it always runs that workspace's
pinned version, like rustup or mise. So `aw` and `bin/aw` behave identically, and upgrading the
global install never changes a workspace's behavior.

## The launcher

`bin/aw` is a committed POSIX shell script of a few dozen lines, managed by `aw`:

1. Find the owner checkout. In a lane root, read the gitignored `.aw/owner` pointer written when the
   lane was created. Otherwise the script's own workspace is the owner checkout.
2. Read the pinned version from the owner checkout's `.aw/version` with `read`, and the expected
   checksum for this platform from `.aw/lock`.
3. If `${XDG_CACHE_HOME:-~/.cache}/agent-workspace/bin/<version>/aw` exists, `exec` it.
4. Otherwise download the release asset (from `AW_MIRROR` if set), verify the checksum, verify the
   signature when `cosign` is available, store it atomically, then `exec` it.

Steps 1 to 3 use only shell builtins, so the launcher adds well under a millisecond to every call.
`AW_TOOL_PATH` points the launcher at a local build, for working on `aw` itself.

Lanes always run the owner checkout's pin, never the version on their own branch. One workspace has
one active version, which keeps the shared state store on one schema (requirement R17).

## Onboarding

### Creating a workspace (a maintainer, once)

```sh
brew install agent-workspace
cd ~/Code/shop-workspace          # or: aw init --new ~/Code/shop-workspace
aw init                           # or: run the workspace-design skill (onboarding.md)
aw add module postgres mail-catcher
aw doctor
aw rehearse
git add -A && git commit -m "Adopt agent-workspace 0.4.2" && git push
```

`aw init` detects projects and stacks and writes `workspace.toml`, `.aw/version`, `.aw/lock`,
`.aw/managed`, `bin/aw`, the harness hook configuration for each supported harness, the generated
`AGENTS.md` block and the `.gitignore` entries. For a multi-repository workspace it records each
project's clone URL.

### Joining a workspace (every team member)

```sh
git clone git@github.com:shop/shop-workspace && cd shop-workspace
bin/aw setup
```

`setup` needs nothing installed beforehand except `sh`, `git`, `curl` or `wget`, and `sha256sum` or
`shasum`. It is idempotent:

1. Fetch and verify the pinned binary.
2. Clone the project repositories.
3. Create the individual layer at `~/.config/agent-workspace/<namespace>.toml`, asking for the default
   harness.
4. Link secrets, or say which secret files are missing and where they come from.
5. Install the Git hooks dispatcher and the harness hook configuration.
6. Install dependencies in the owner checkout.
7. Offer to start shared infrastructure.
8. Run `aw doctor`.

Then `aw new "<purpose>"`, or start a harness directly.

### Adding a project later

`aw add project <alias> <repo-url>` updates `workspace.toml`, `.gitignore` and the generated
instructions, clones the repository and runs its install. Running the readiness audit on the new
project is recommended ([onboarding.md](onboarding.md)).

## Upgrades

### The maintainer

```sh
git checkout -b upgrade-aw
aw upgrade                 # to the latest; or: aw upgrade --to 0.5.0
```

`aw upgrade`:

1. Shows the changelog between the pinned and the target version, including any breaking changes.
2. Rewrites `.aw/version` and `.aw/lock`.
3. Migrates `workspace.toml` when the configuration schema changed, and shows the diff.
4. Regenerates managed files: the launcher, hook configuration, generated instruction blocks and
   module files. A managed file that was edited by hand is not overwritten; it is listed with the
   choices (keep the edit and eject the file, or take the new version).
5. Runs `aw doctor`.

The result is committed and goes through a normal merge request. That is the human review that
changes to the workspace's own configuration require.

The project will publish a Renovate preset and a Dependabot-compatible manifest so new releases
arrive as merge requests that bump the pin.

### Everyone else

`git pull`. The next `bin/aw` call fetches and verifies the new version once. Every call after that
is as fast as before.

### Activation and state migration

When a new version first runs against a workspace's state store:

1. It takes the state lock, backs up `state.db` to `state.db.bak-<old schema>` and migrates forward
   in one transaction.
2. Running sessions pick up the new version at their next tool call, because every hook goes through
   the launcher.
3. Each release must read and migrate the previous state schema. Releases may skip a schema version
   only if they ship every intermediate migration.
4. A binary that finds a state schema newer than it knows (someone ran a newer version, then pulled
   an older pin) goes read-only: it reports the situation and the fix, and protects every lane.

The new version takes effect at once. A `--drain` option, which would wait until no lane holds a
lease, is added only if a future state migration cannot run safely while leases are held (see
[README.md](README.md#settled-questions)).

### Compatibility promise

- Patch releases: fixes only. No schema changes.
- Minor releases: may add configuration keys and migrate state automatically. Existing configuration
  keeps working.
- Major releases: may remove or rename configuration. `aw upgrade` migrates what it can and lists what
  it cannot.
- Harness payload formats are not under our control. When a harness changes, its adapter fails
  closed until a release supports the new format, and the error says so.
