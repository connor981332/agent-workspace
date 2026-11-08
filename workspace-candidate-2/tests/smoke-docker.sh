#!/bin/sh
# End-to-end check against real Docker and the real devcontainer CLI. Not run
# by CI (it needs Docker, network access for images and npx, and a minute).
#
#   tests/smoke-docker.sh <empty scratch dir>
#
# Makes a workspace "awc2smoke" (Compose projects awc2smoke-infra and
# app_devcontainer, network awc2smoke-dev-net, host ports 13000-13004, 35432,
# 36379), exercises containers, lanes, and snapshots, then removes everything
# it created, including volumes. Pick another directory name than "app" if a
# project of yours uses app_devcontainer.

set -eu
[ $# -eq 1 ] || { echo "usage: $0 <empty scratch dir>" >&2; exit 2; }
KIT="$(cd "$(dirname "$0")/.." && pwd -P)/kit"
mkdir -p "$1"; S="$(cd "$1" && pwd -P)"
[ -z "$(ls -A "$S")" ] || { echo "$S is not empty" >&2; exit 2; }
W="$S/ws"

cleanup() {
  set +e
  echo "--- cleanup"
  docker compose -p app_devcontainer down -v --remove-orphans >/dev/null 2>&1
  docker ps -aq --filter label=com.docker.compose.project=app_devcontainer | xargs -r docker rm -f >/dev/null
  COMPOSE_FILE="$W/infra/compose.yaml" docker compose -p awc2smoke-infra down -v --remove-orphans >/dev/null 2>&1
  docker network rm awc2smoke-dev-net >/dev/null 2>&1
  docker images -q --filter "reference=app_devcontainer*" | xargs -r docker rmi -f >/dev/null 2>&1
}
trap cleanup EXIT

step() { echo "--- $*"; }
check() { # <what> <expected substring> <actual>
  case "$3" in *"$2"*) echo "ok: $1" ;; *) echo "FAILED: $1: expected [$2] in [$3]" >&2; exit 1 ;; esac
}

step "make the workspace and a project repo"
git init -q --bare "$S/remotes/app.git"
git -C "$S/remotes/app.git" symbolic-ref HEAD refs/heads/main
git clone -q "$S/remotes/app.git" "$S/seed"
git -C "$S/seed" checkout -q -b main
mkdir -p "$S/seed/config"
echo 'database: app_development${WS_LANE}' > "$S/seed/config/database.yml"
printf '.env.local\n' > "$S/seed/.gitignore"
git -C "$S/seed" add -A
git -C "$S/seed" -c user.name=t -c user.email=t@example.invalid commit -qm init
git -C "$S/seed" push -q origin main
"$KIT" init "$W" --name awc2smoke --remote "$S/remotes" --port-offset 30000 --checkout "$W" >/dev/null
"$W/bin/bootstrap" >/dev/null 2>&1 || true   # no projects yet; checks the path
git clone -q "$S/remotes/app.git" "$W/app"
"$KIT" add-project "$W" app app --lanes worktree --port 13000 --user postgres >/dev/null
# A tiny dev image: Postgres' image has sh and psql.
printf 'FROM postgres:18-alpine\nUSER postgres\n' > "$W/app/.devcontainer/Dockerfile"
sed -i.bak 's/^WS_LANE_GUARDS=.*/WS_LANE_GUARDS="app:config\/database.yml:WS_LANE"/' "$W/workspace.conf"
"$KIT" check "$W"

step "start the project container through bin/ws (devcontainer up, claim, infra)"
"$W/bin/ws" app --up
check "infra running" "postgres" "$(docker ps --filter label=com.docker.compose.project=awc2smoke-infra --format '{{.Label "com.docker.compose.service"}}')"
check "container labelled with its checkout" "$W/app" "$(docker ps --filter label=com.docker.compose.project=app_devcontainer --format '{{.Label "devcontainer.local_folder"}}')"
check "exec in the workspace folder" "/app" "$("$W/bin/ws" app pwd </dev/null)"
check "shared postgres reachable" "1" "$("$W/bin/ws" app psql postgres://postgres:password@postgres:5432/postgres -Atc 'select 1' </dev/null)"

step "warm start skips Compose"
out="$(cd "$W/app" && "$W/bin/devcontainer-init")"
check "no compose output on a warm start" "" "$out"
[ -z "$out" ] || { echo "FAILED: warm start ran Compose: $out" >&2; exit 1; }

step "a lane"
"$W/bin/lane" new l1 feature/smoke
check "lane env in the container" "l1 13001 redis://redis:6379/1 /app/.worktrees/l1" \
  "$("$W/bin/ws" app@l1 sh -c 'echo $WS_LANE $PORT $REDIS_URL $(pwd)' </dev/null)"
check "worktree visible in the container" "WS_LANE" "$("$W/bin/ws" app@l1 cat config/database.yml </dev/null)"
check "lane databases created" "awc2smoke_test_l1" "$("$W/bin/ws" app psql postgres://postgres:password@postgres:5432/postgres -Atc 'select datname from pg_database' </dev/null)"
"$W/bin/ws" app psql postgres://postgres:password@postgres:5432/awc2smoke_development_l1 -qc 'create table t (v int); insert into t values (42)' </dev/null

step "snapshot and restore"
"$W/bin/lane" snapshot l1 seeded
"$W/bin/ws" app psql postgres://postgres:password@postgres:5432/awc2smoke_development_l1 -qc 'delete from t' </dev/null
"$W/bin/lane" restore l1 seeded
check "restored row" "42" "$("$W/bin/ws" app psql postgres://postgres:password@postgres:5432/awc2smoke_development_l1 -Atc 'select v from t' </dev/null)"
check "snapshot listed" "seeded" "$("$W/bin/lane" snapshots)"

step "status"
"$W/bin/ws" status

step "remove the lane and its data"
"$W/bin/lane" rm l1 --drop-data
dbs="$("$W/bin/ws" app psql postgres://postgres:password@postgres:5432/postgres -Atc 'select datname from pg_database' </dev/null)"
case "$dbs" in *_l1*) echo "FAILED: lane databases left: $dbs" >&2; exit 1 ;; esac
echo "ok: lane databases dropped"
[ ! -d "$W/app/.worktrees/l1" ] && echo "ok: worktree removed"

step "claim: a container from a moved checkout is replaced"
docker stop -t 1 "$(docker ps -q --filter label=com.docker.compose.project=app_devcontainer)" >/dev/null
mv "$W" "$S/moved" && mkdir -p "$W" && rmdir "$W" && mv "$S/moved" "$S/ws2"
W="$S/ws2"
sed -i.bak "s|^WS_CHECKOUT=.*|WS_CHECKOUT=$W|" "$W/workspace.conf"
"$W/bin/ws" app --up 2>&1 | grep -E "Removing|Starting" || true
check "new container mounts the new path" "$W/app" "$(docker ps --filter label=com.docker.compose.project=app_devcontainer --format '{{.Label "devcontainer.local_folder"}}')"
check "workspace visible" "database.yml" "$("$W/bin/ws" app ls config </dev/null)"

echo "--- smoke test passed"
