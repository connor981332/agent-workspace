# Upgrading

A workspace pins one exact version of `aw` in `.aw/version`. Moving to another is a change like any
other, and goes through a merge request.

```sh
git checkout -b upgrade-aw
aw upgrade                 # to the latest; or: aw upgrade --to 0.5.0
```

`aw upgrade` shows the changelog, including anything breaking, rewrites `.aw/version` and `.aw/lock`,
migrates `workspace.toml` and shows the diff, regenerates managed files, and runs `aw doctor`. If the
new binary cannot be fetched and verified, the workspace stays where it was.

Commit the result. Everyone else gets it with `git pull`: the next `bin/aw` fetches and verifies the
new version once. Running sessions and existing lanes move to it at their next tool call. The first
command of a new version backs up the state to `state.db.bak-<schema>` and migrates it.

`aw upgrade` only goes forward. To go back, restore `.aw/version` and `.aw/lock` from Git. A binary
that finds state newer than it knows goes read-only and protects every lane.

## A managed file you edited

`aw` never overwrites a managed file that was edited by hand. It lists the file, and you choose, by
running `aw upgrade` again at the same version:

```sh
aw upgrade --to 0.5.0 --eject bin/aw    # keep your edit, and stop aw managing the file
aw upgrade --to 0.5.0 --take bin/aw     # replace it with the new version
```

A hand edit to the generated block in `AGENTS.md` is reported the same way: move the change to
`.aw/instructions.md`, delete the block, and run `aw setup`.

## Automatic merge requests

With Renovate, extend `github>connor981332/agent-workspace` in your configuration. A pin needs its
checksums and managed files regenerated as well, so the preset runs `bin/aw upgrade` through
`postUpgradeTasks`. Those run only on a **self-hosted** Renovate that allows that command. The hosted
Mend Renovate app cannot run them, so there it would bump `.aw/version` without `.aw/lock`, and
`bin/aw` would refuse the pin. Do not use the preset with the hosted app.
