# bin/ws and bin/devcontainer-init, against the docker stub.

t_ws_starts_missing_container_then_execs() {
  make_workspace
  "$W/bin/ws" app echo hi </dev/null >/dev/null 2>&1 || fail "bin/ws app failed: $(log)"
  assert_contains "$(log)" "devcontainer up --workspace-folder $W/web" "starts through the devcontainer CLI"
  assert_contains "$(log)" "docker exec -i -w /web c-web echo hi" "execs in the main checkout"
}

t_ws_up_only() {
  make_workspace
  "$W/bin/ws" app --up >/dev/null 2>&1 </dev/null
  assert_not_contains "$(log)" "docker exec" "--up runs nothing"
}

t_ws_lane_uses_worktree_env_and_port() {
  make_workspace
  "$W/bin/lane" new l1 feature/x >/dev/null 2>&1 || fail "lane new: $(cat "$T/out" 2>/dev/null)"
  : > "$STUB_LOG"
  "$W/bin/ws" app@l1 bin/test </dev/null >/dev/null 2>&1
  assert_contains "$(log)" "docker exec -i --env-file $W/.lanes/l1.env -e PORT=3001 -w /web/.worktrees/l1 c-web bin/test" "lane exec"
  : > "$STUB_LOG"
  "$W/bin/ws" worker@l1 run </dev/null >/dev/null 2>&1
  assert_contains "$(log)" "docker exec -i --env-file $W/.lanes/l1.env -w /jobs c-jobs run" "shared project in a lane: main checkout, no PORT"
}

t_ws_rejects_unknown() {
  make_workspace
  assert_fails "unknown alias" "$W/bin/ws" nope true
  assert_fails "unknown lane" "$W/bin/ws" app@zz true
  assert_fails "infra down" "$W/bin/ws" infra down
  "$W/bin/ws" infra ps >/dev/null 2>&1
  assert_contains "$(log)" "docker compose -p shop-infra ps" "infra passthrough"
}

t_init_claims_stale_container_and_starts_infra() {
  make_workspace
  echo old1 > "$STUB_STATE/ps/web_devcontainer__web"
  echo "/gone/web" > "$STUB_STATE/mount/old1"
  echo true > "$STUB_STATE/state/old1"
  mkdir -p "$T/ws/infra/compose.d" && echo "services: {}" > "$W/infra/compose.d/extra.yaml"
  (cd "$W/web" && "$W/bin/devcontainer-init") >"$T/out" 2>&1 || fail "init failed: $(cat "$T/out")"
  assert_contains "$(log)" "docker rm -f old1" "removes the container of a vanished checkout"
  assert_contains "$(log)" "docker network create shop-dev-net" "creates the network"
  assert_contains "$(log)" "docker compose -p shop-infra up -d --remove-orphans" "starts infrastructure"
  assert_contains "$(log)" "COMPOSE_FILE=$W/infra/compose.yaml:$W/infra/compose.d/extra.yaml" "includes compose.d"
}

t_init_refuses_live_other_checkout() {
  make_workspace
  mkdir -p "$T/other/web" && echo x > "$T/other/web/f"
  echo live1 > "$STUB_STATE/ps/web_devcontainer__web"
  echo "$T/other/web" > "$STUB_STATE/mount/live1"
  echo true > "$STUB_STATE/state/live1"
  assert_fails "live container from another checkout" sh -c "cd '$W/web' && '$W/bin/devcontainer-init'"
  assert_contains "$(cat "$T/out")" "running from another checkout" "says why"
  assert_not_contains "$(log)" "docker rm" "doesn't remove it"
}

t_init_skips_compose_when_infra_runs() {
  make_workspace
  mkdir -p "$STUB_STATE/net" "$STUB_STATE/running" && touch "$STUB_STATE/net/shop-dev-net"
  printf 'postgres\nredis\n' > "$STUB_STATE/running/shop-infra"
  (cd "$W/web" && "$W/bin/devcontainer-init") >/dev/null 2>&1
  assert_not_contains "$(log)" "docker compose" "warm start skips Compose"
  printf 'postgres\nminio\n' > "$STUB_STATE/running/shop-infra"
  (cd "$W/web" && "$W/bin/devcontainer-init") >/dev/null 2>&1
  assert_contains "$(log)" "docker compose" "a missing service starts Compose"
}

t_relpath() {
  . "$CANDIDATE/core/lib/ws/common.sh"
  assert_eq "../../.env" "$(ws_relpath /w/web/.worktrees/l1 /w/web/.env)" "sibling up two"
  assert_eq "../../../config/k" "$(ws_relpath /w/web/.worktrees/l1/config /w/web/config/k)" "nested"
  assert_eq "b" "$(ws_relpath /a /a/b)" "child"
}
