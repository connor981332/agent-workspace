# Workspace lane resources

Put a lane resource adapter here as `<name>.sh` and add `<name>` to `WS_LANE_RESOURCES` in
`workspace.conf`. An adapter here wins over a core adapter of the same name in `lib/ws/resources/`,
and `kit update` leaves this directory alone.

An adapter defines any of these functions; `bin/lane` calls the ones that exist:

| Function | Called by | Does |
|---|---|---|
| `res_<name>_env <lane> <slot>` | `lane new` | Prints `KEY=value` lines for the lane's env file |
| `res_<name>_create <lane> <slot>` | `lane new` | Makes the lane's copy |
| `res_<name>_drop <lane> <slot>` | `lane rm --drop-data` | Deletes it, by name |
| `res_<name>_snapshot <lane> <snap>` | `lane snapshot` | Saves it under a name |
| `res_<name>_restore <lane> <snap>` | `lane restore` | Replaces it with a saved copy |
| `res_<name>_snapshots` | `lane snapshots` | Lists saved copies |
| `res_<name>_describe <lane> <slot>` | `lane show` | One line about the lane's copy |

Helpers from `lib/ws/common.sh` are available: `ws_infra_container <service>`, `ws_die`, `ws_warn`,
and every `WS_*` setting.
