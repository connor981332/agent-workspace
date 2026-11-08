# Shared helpers for the workspace scripts in bin/. POSIX sh; sourced, never run.
#
# This file is part of the workspace core: `kit update` replaces it. Nothing in
# it names a project. Everything specific to a workspace comes from
# workspace.conf, the projects file, infra/, and lanes.d/, which the workspace
# owns.
#
# Callers set ROOT (the workspace root, resolved with pwd -P) before sourcing.

ws_die() { echo "${WS_SCRIPT:-ws}: $*" >&2; exit "${WS_EXIT:-1}"; }
ws_usage_die() { WS_EXIT=2 ws_die "$@"; }
ws_warn() { echo "${WS_SCRIPT:-ws}: $*" >&2; }

# --- Configuration -----------------------------------------------------------
# workspace.conf is a plain sh file of VAR=value lines. Defaults fill anything
# it leaves out, so an older workspace.conf keeps working after `kit update`.
ws_load_conf() {
  [ -f "$ROOT/workspace.conf" ] || ws_die "no workspace.conf in $ROOT"
  # shellcheck disable=SC1091
  . "$ROOT/workspace.conf"
  : "${WS_NAME:?workspace.conf must set WS_NAME}"
  : "${WS_PREFIX:=$WS_NAME}"
  : "${WS_NETWORK:=${WS_PREFIX}-dev-net}"
  : "${WS_INFRA_PROJECT:=${WS_PREFIX}-infra}"
  : "${WS_BASE_BRANCH:=main}"
  : "${WS_REMOTE:=}"
  : "${WS_CHECKOUT:=}"
  : "${WS_DEVCONTAINER_CLI:=npx -y @devcontainers/cli@0.89.0}"
  : "${WS_DEFAULT_SHELL:=sh}"
  : "${WS_INFRA_SERVICES:=}"
  : "${WS_LANE_SLOTS:=4}"
  : "${WS_LANE_RESOURCES:=}"
  : "${WS_LANE_VAR:=}"
  : "${WS_LANE_GUARDS:=}"
  : "${WS_LANE_LINKS:=}"
  WS_LANES_DIR="$ROOT/.lanes"
}

# --- Projects ----------------------------------------------------------------
# The projects file: one project per line, `<alias> <directory> [lanes] [port]`.
#   lanes  worktree  a lane gets its own git worktree of this repo
#          shared    a lane runs in the main checkout, with the lane's settings
#   port   the dev server's port inside the container, or - for none. Lane in
#          slot N gets port + N, published on the same host port.
# Printed normalized: four fields per line, comments and blank lines removed.
ws_projects() {
  sed -e 's/#.*//' "$ROOT/projects" | awk 'NF {
    lanes = (NF >= 3) ? $3 : "shared"; port = (NF >= 4) ? $4 : "-"
    print $1, $2, lanes, port }'
}

# ws_project <alias> sets P_ALIAS P_DIR P_LANES P_PORT, or returns 1.
ws_project() {
  line="$(ws_projects | awk -v a="$1" '$1 == a')"
  [ -n "$line" ] || return 1
  # shellcheck disable=SC2086
  set -- $line
  P_ALIAS=$1 P_DIR=$2 P_LANES=$3 P_PORT=$4
}

ws_aliases() { ws_projects | awk '{print $1}' | paste -sd '|' -; }

# --- Containers --------------------------------------------------------------
# A project's dev container is found by the labels Compose and the devcontainer
# CLI put on it, never by container name: <dir>_devcontainer is the Compose
# project (pinned in the project's compose.yaml), <dir> the service, and the
# host checkout path the devcontainer.local_folder label.
ws_project_container() { # <dir> [all]
  _flag=-q; [ "${2:-}" = all ] && _flag=-aq
  docker ps $_flag \
    --filter "label=com.docker.compose.project=${1}_devcontainer" \
    --filter "label=com.docker.compose.service=$1" \
    --filter "label=devcontainer.local_folder=$ROOT/$1"
}

ws_infra_container() { # <service>: the running container of a shared service
  docker ps -q \
    --filter "label=com.docker.compose.project=$WS_INFRA_PROJECT" \
    --filter "label=com.docker.compose.service=$1" | head -n 1
}

# docker compose on the shared infrastructure: infra/compose.yaml, then every
# infra/compose.d/*.yaml in name order (modules add services there). Relative
# paths in every file resolve against infra/, the first file's directory.
ws_infra_compose() {
  _cf="$ROOT/infra/compose.yaml"
  for f in "$ROOT"/infra/compose.d/*.yaml; do
    if [ -f "$f" ]; then _cf="$_cf:$f"; fi
  done
  COMPOSE_FILE="$_cf" COMPOSE_PATH_SEPARATOR=: docker compose -p "$WS_INFRA_PROJECT" "$@"
}

# --- Lanes -------------------------------------------------------------------
ws_valid_lane() {
  echo "$1" | grep -Eq '^[a-z][a-z0-9]{0,15}$' ||
    ws_usage_die "lane names are lowercase letters and digits, up to 16, starting with a letter"
}

ws_lane_file() { echo "$WS_LANES_DIR/$1.env"; }

ws_lane_exists() { [ -f "$(ws_lane_file "$1")" ]; }

ws_need_lane() {
  ws_valid_lane "$1"
  ws_lane_exists "$1" || ws_usage_die "no lane '$1' (bin/lane list)"
}

# ws_lane_get <lane> <KEY>: a value from the lane's env file. The file is read,
# never sourced, because Docker reads it too and the formats differ on quoting.
ws_lane_get() { sed -n "s/^$2=//p" "$(ws_lane_file "$1")" | head -n 1; }

# The lane's checkout of a project: its worktree if the project has lane
# worktrees, otherwise the main checkout.
ws_lane_tree() { # <dir> <lane>
  if [ -n "$2" ] && [ -d "$ROOT/$1/.worktrees/$2" ]; then
    echo "$ROOT/$1/.worktrees/$2"
  else
    echo "$ROOT/$1"
  fi
}

# --- Lane resources ----------------------------------------------------------
# A resource adapter is a sh file defining res_<name>_<hook> functions. Core
# adapters live in lib/ws/resources/; a workspace adds its own in
# lanes.d/resources/ (kept by `kit update`). Hooks, all optional:
#   env <lane> <slot>        print KEY=value lines for the lane's env file
#   create <lane> <slot>     make the lane's copy of the resource
#   drop <lane> <slot>       delete it (bin/lane rm --drop-data)
#   snapshot <lane> <snap>   save it under a name
#   restore <lane> <snap>    replace it with a saved copy
#   snapshots                list saved copies, one per line
#   describe <lane> <slot>   one line for bin/lane show
ws_load_resources() {
  for r in $WS_LANE_RESOURCES; do
    found=""
    for f in "$ROOT/lanes.d/resources/$r.sh" "$ROOT/lib/ws/resources/$r.sh"; do
      if [ -f "$f" ]; then
        # shellcheck disable=SC1090
        . "$f"; found=1; break
      fi
    done
    [ -n "$found" ] || ws_die "lane resource '$r' (WS_LANE_RESOURCES) has no adapter in lib/ws/resources or lanes.d/resources"
  done
}

ws_res_has() { command -v "res_$1_$2" >/dev/null 2>&1; }

ws_res_call() { # <resource> <hook> args...
  _r=$1 _h=$2; shift 2
  if ws_res_has "$_r" "$_h"; then "res_${_r}_${_h}" "$@"; fi
}

# --- Paths -------------------------------------------------------------------
# ws_relpath <from-dir> <to-path>: a relative path from one absolute directory
# to another absolute path, for symlinks that must resolve inside a container
# that mounts the checkout at a different path.
ws_relpath() {
  _from="${1%/}/" _to="$2" _up=""
  while [ "${_to#"$_from"}" = "$_to" ]; do
    _from="${_from%/*/}/"
    _up="../$_up"
  done
  echo "$_up${_to#"$_from"}"
}
