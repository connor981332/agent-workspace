# bin/lane with real git repos and the docker stub.

t_lane_new_makes_worktree_env_and_links() {
  make_workspace
  "$W/bin/lane" new l1 feature/x >"$T/out" 2>&1 || { fail "lane new: $(cat "$T/out")"; return; }
  assert_eq "feature/x" "$(git -C "$W/web/.worktrees/l1" branch --show-current)" "worktree branch"
  assert_no_file "$W/jobs/.worktrees/l1"
  env="$(cat "$W/.lanes/l1.env")"
  assert_contains "$env" "WS_LANE=l1" "lane var"
  assert_contains "$env" "WS_SLOT=1" "slot"
  assert_contains "$env" "REDIS_URL=redis://redis:6379/1" "redis adapter env"
  assert_eq "../.env.development.local" "$(readlink "$W/web/.worktrees/l1/.env.development.local" | sed 's|^\.\./||')" "relative secret link" 2>/dev/null
  assert_eq "SECRET=1" "$(cat "$W/web/.worktrees/l1/.env.development.local")" "secret link resolves"
  assert_eq "key" "$(cat "$W/web/.worktrees/l1/config/master.key")" "nested link resolves"
  case "$(readlink "$W/web/.worktrees/l1/config/master.key")" in /*) fail "link is absolute" ;; esac
  assert_contains "$(log)" "CREATE DATABASE \"shop_development_l1\"" "postgres adapter creates databases"
  perms="$(ls -l "$W/.lanes/l1.env" | cut -c1-10)"
  assert_eq "-rw-------" "$perms" "env file private"
  assert_contains "$(cat "$T/out")" "ports: app=3001" "summary"
}

t_lane_slots_and_limits() {
  make_workspace
  sed -i.bak 's/^WS_LANE_SLOTS=4/WS_LANE_SLOTS=2/' "$W/workspace.conf"
  "$W/bin/lane" new aa a >/dev/null 2>&1
  "$W/bin/lane" new bb b >/dev/null 2>&1
  assert_eq "WS_SLOT=2" "$(grep WS_SLOT "$W/.lanes/bb.env")" "second slot"
  assert_fails "slots exhausted" "$W/bin/lane" new cc c
  assert_contains "$(cat "$T/out")" "all 2 slots are taken" "says why"
  assert_fails "duplicate" "$W/bin/lane" new aa a
  assert_fails "bad name" "$W/bin/lane" new Bad x
  "$W/bin/lane" rm aa >/dev/null 2>&1 || fail "rm aa"
  "$W/bin/lane" new cc c >/dev/null 2>&1 || fail "freed slot is reused"
  assert_eq "WS_SLOT=1" "$(grep WS_SLOT "$W/.lanes/cc.env")" "reuses slot 1"
  list="$("$W/bin/lane" list)"
  assert_contains "$list" "bb	slot 2	b	ports: app=3002" "list"
}

t_lane_guard_refuses_old_branch() {
  make_workspace
  git -C "$W/web" checkout -q -b old
  echo "database: shop_development" > "$W/web/config/database.yml"
  git -C "$W/web" commit -qam old && git -C "$W/web" push -q origin old
  git -C "$W/web" checkout -q main
  assert_fails "guard" "$W/bin/lane" new l1 old
  assert_contains "$(cat "$T/out")" "predates lanes" "says why"
  assert_no_file "$W/web/.worktrees/l1"
  assert_no_file "$W/.lanes/l1.env"
}

t_lane_existing_remote_branch_tracks() {
  make_workspace
  git -C "$W/web" checkout -q -b shared/x && git -C "$W/web" push -q origin shared/x && git -C "$W/web" checkout -q main
  git -C "$W/web" branch -qD shared/x
  "$W/bin/lane" new l1 shared/x >/dev/null 2>&1 || fail "lane on remote branch"
  assert_eq "origin/shared/x" "$(git -C "$W/web/.worktrees/l1" rev-parse --abbrev-ref '@{u}')" "tracks the remote"
}

t_lane_rm_protects_unsaved_work() {
  make_workspace
  "$W/bin/lane" new l1 feature/x >/dev/null 2>&1
  echo change > "$W/web/.worktrees/l1/new.txt"
  assert_fails "dirty" "$W/bin/lane" rm l1
  assert_contains "$(cat "$T/out")" "uncommitted changes" "dirty reason"
  git -C "$W/web/.worktrees/l1" add new.txt && git -C "$W/web/.worktrees/l1" commit -qm work
  assert_fails "unpushed" "$W/bin/lane" rm l1
  assert_contains "$(cat "$T/out")" "neither pushed nor merged" "unpushed reason"
  git -C "$W/web/.worktrees/l1" push -q origin feature/x
  "$W/bin/lane" rm l1 >/dev/null 2>&1 || fail "pushed lane removes"
  assert_no_file "$W/web/.worktrees/l1"
  assert_no_file "$W/.lanes/l1.env"
  assert_not_contains "$(log)" "DROP DATABASE" "data kept without --drop-data"
}

t_lane_rm_force_and_drop_data() {
  make_workspace
  "$W/bin/lane" new l1 feature/x >/dev/null 2>&1
  echo change > "$W/web/.worktrees/l1/new.txt"
  echo shop_test_l1_0 > "$STUB_STATE/psql-select"
  "$W/bin/lane" rm l1 --drop-data --force >"$T/out" 2>&1 || fail "forced rm: $(cat "$T/out")"
  for db in shop_development_l1 shop_test_l1 shop_test_l1_0; do
    assert_contains "$(log)" "DROP DATABASE IF EXISTS \"$db\"" "drops $db"
  done
  assert_contains "$(log)" "docker exec rd1 redis-cli -n 1 FLUSHDB" "flushes the lane's redis db"
  assert_no_file "$W/web/.worktrees/l1"
}

t_lane_snapshot_restore() {
  make_workspace
  "$W/bin/lane" new l1 feature/x >/dev/null 2>&1
  "$W/bin/lane" snapshot l1 onboarded >/dev/null 2>&1 || fail "snapshot"
  assert_contains "$(log)" "CREATE DATABASE \"shop_snapshot_onboarded\" TEMPLATE \"shop_development_l1\"" "snapshot SQL"
  : > "$STUB_LOG"
  echo 1 > "$STUB_STATE/psql-select"
  "$W/bin/lane" restore l1 onboarded >/dev/null 2>&1 || fail "restore"
  assert_contains "$(log)" "DROP DATABASE IF EXISTS \"shop_development_l1\"" "restore drops"
  assert_contains "$(log)" "CREATE DATABASE \"shop_development_l1\" TEMPLATE \"shop_snapshot_onboarded\"" "restore copies"
  rm "$STUB_STATE/psql-select"
  assert_fails "missing snapshot" "$W/bin/lane" restore l1 nothere
}

t_lane_setup_hook_and_custom_resource() {
  make_workspace
  cat > "$W/lanes.d/setup" <<'H'
#!/bin/sh
echo "setup $WS_LANE $WS_SLOT $1" >> "$STUB_LOG"
H
  chmod +x "$W/lanes.d/setup"
  cat > "$W/lanes.d/resources/queue.sh" <<'R'
res_queue_env() { echo "QUEUE=q$2"; }
res_queue_describe() { echo "queue q$2"; }
R
  sed -i.bak 's/^WS_LANE_RESOURCES=.*/WS_LANE_RESOURCES="postgres redis queue"/' "$W/workspace.conf"
  "$W/bin/lane" new l1 feature/x >/dev/null 2>&1 || fail "lane new"
  assert_contains "$(log)" "setup l1 1 l1" "setup hook runs with the lane's env"
  assert_contains "$(cat "$W/.lanes/l1.env")" "QUEUE=q1" "workspace adapter env"
  assert_contains "$("$W/bin/lane" show l1)" "queue: queue q1" "show describes resources"
  sed -i.bak 's/^WS_LANE_RESOURCES=.*/WS_LANE_RESOURCES="nosuch"/' "$W/workspace.conf"
  assert_fails "unknown resource" "$W/bin/lane" list
}

t_lane_var_alias() {
  make_workspace
  echo 'WS_LANE_VAR=APP_LANE' >> "$W/workspace.conf"
  "$W/bin/lane" new l1 feature/x >/dev/null 2>&1
  assert_contains "$(cat "$W/.lanes/l1.env")" "APP_LANE=l1" "alias of the lane variable"
}

t_lane_usage() {
  make_workspace
  assert_fails "no command" "$W/bin/lane"
  assert_contains "$(cat "$T/out")" "bin/lane new <name> <branch>" "usage text"
  assert_contains "$(cat "$T/out")" "bin/lane rm <name>" "usage text ends with rm"
}
