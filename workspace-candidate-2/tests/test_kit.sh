# kit: init, update, add-project, add-module, check.

owned_files() { # files rendered from template/ (everything but core and .git)
  (cd "$1" && find . -type f ! -path './.git/*' ! -path './bin/*' ! -path './lib/*' | sed 's|^\./||')
}

t_init_renders_every_placeholder() {
  "$KIT" init "$T/w" --name shop --title "Shop & Co" --port-offset 10 >/dev/null
  for f in $(owned_files "$T/w"); do
    grep -q '{{' "$T/w/$f" && fail "placeholder left in $f: $(grep -n '{{' "$T/w/$f" | head -n 1)"
  done
  assert_contains "$(head -n 1 "$T/w/AGENTS.md")" "Shop & Co" "title with &"
  assert_contains "$(cat "$T/w/infra/compose.yaml")" '"5442:5432"' "port offset"
  assert_contains "$(cat "$T/w/workspace.conf")" "WS_PREFIX=shop" "prefix defaults to name"
  assert_file "$T/w/.git"
}

t_init_copies_core_exactly() {
  "$KIT" init "$T/w" --name shop >/dev/null
  for f in bin/ws bin/lane bin/bootstrap bin/devcontainer-init lib/ws/common.sh lib/ws/resources/postgres.sh lib/ws/resources/redis.sh; do
    cmp -s "$CANDIDATE/core/$f" "$T/w/$f" || fail "core $f differs"
  done
  [ -x "$T/w/bin/ws" ] || fail "bin/ws not executable"
}

t_init_refuses_existing_workspace() {
  "$KIT" init "$T/w" --name shop >/dev/null
  assert_fails "second init" "$KIT" init "$T/w" --name shop
  assert_fails "bad name" "$KIT" init "$T/x" --name "Bad Name"
}

t_scripts_parse() {
  for f in "$KIT" "$CANDIDATE"/core/bin/* "$CANDIDATE"/core/lib/ws/*.sh "$CANDIDATE"/core/lib/ws/resources/*.sh \
           "$CANDIDATE"/modules/webhook-forward/files/bin/* "$CANDIDATE"/modules/webhook-forward/files/lanes.d/webhooks/*.sh; do
    sh -n "$f" || fail "syntax: $f"
  done
}

t_update_refreshes_core_and_keeps_owned() {
  "$KIT" init "$T/w" --name shop >/dev/null
  echo "# local" >> "$T/w/bin/lane"
  echo "WS_LANE_SLOTS=2" >> "$T/w/workspace.conf"
  echo "edited" >> "$T/w/AGENTS.md"
  out="$("$KIT" update "$T/w")"
  assert_contains "$out" "core bin/lane" "update reports the changed core file"
  cmp -s "$CANDIDATE/core/bin/lane" "$T/w/bin/lane" || fail "bin/lane not restored"
  assert_contains "$(cat "$T/w/workspace.conf")" "WS_LANE_SLOTS=2" "workspace.conf kept"
  assert_contains "$(cat "$T/w/AGENTS.md")" "edited" "AGENTS.md kept"
}

t_add_project_wires_everything() {
  "$KIT" init "$T/w" --name shop >/dev/null
  mkdir "$T/w/web"
  "$KIT" add-project "$T/w" app web --lanes worktree --port 3000 --host-port 3200 --user rails >/dev/null
  assert_contains "$(sed 's/#.*//' "$T/w/projects")" "app      web" "projects line"
  grep -qx "/web/" "$T/w/.gitignore" || fail ".gitignore lacks /web/"
  c="$(cat "$T/w/web/.devcontainer/compose.yaml")"
  assert_contains "$c" "name: web_devcontainer" "pinned compose project"
  assert_contains "$c" '"3200:3000"' "main port"
  assert_contains "$c" '"3001-3004:3001-3004"' "lane ports"
  assert_contains "$c" "../../docs:/shop-workspace/docs:ro" "docs mount"
  assert_contains "$(cat "$T/w/web/.devcontainer/devcontainer.json")" '"remoteUser": "rails"' "user"
  assert_contains "$(cat "$T/w/AGENTS.md")" '| `app` | `web/` |' "AGENTS row"
  assert_contains "$(cat "$T/w/docs/development/setup.md")" "http://localhost:3200 (lanes: 3001 … 3004)" "ports row"
  assert_fails "duplicate alias" "$KIT" add-project "$T/w" app other
  assert_fails "bad lanes" "$KIT" add-project "$T/w" b other --lanes sometimes
}

t_add_project_without_port_or_clone() {
  "$KIT" init "$T/w" --name shop >/dev/null
  "$KIT" add-project "$T/w" worker jobs >/dev/null
  c="$(cat "$T/w/.kit/project-templates/jobs/.devcontainer/compose.yaml")"
  assert_not_contains "$c" "ports:" "no ports for a portless project"
}

t_every_module_installs() {
  for m in "$CANDIDATE"/modules/*/; do
    m="$(basename "$m")"
    rm -rf "$T/w"
    "$KIT" init "$T/w" --name shop >/dev/null
    "$KIT" add-module "$T/w" "$m" >"$T/out" 2>&1 || { fail "module $m: $(cat "$T/out")"; continue; }
    for f in $(owned_files "$T/w"); do
      grep -v '{{\.' "$T/w/$f" | grep -q '{{' && fail "module $m left a placeholder in $f"
    done
    assert_contains "$(cat "$T/w/.workspace-kit")" "$m" "module $m recorded"
    assert_fails "module $m twice" "$KIT" add-module "$T/w" "$m"
  done
}

t_module_effects() {
  "$KIT" init "$T/w" --name shop >/dev/null
  "$KIT" add-module "$T/w" mail-catcher >/dev/null
  "$KIT" add-module "$T/w" dns-standin >/dev/null
  "$KIT" add-module "$T/w" browser-mcp >/dev/null
  assert_contains "$(grep '^WS_INFRA_SERVICES=' "$T/w/workspace.conf")" '"dns mailpit postgres redis"' "infra services appended"
  assert_file "$T/w/infra/compose.d/mailpit.yaml"
  assert_file "$T/w/infra/dns/shop-dev.test.zone"
  assert_contains "$(cat "$T/w/docs/development/setup.md")" "Mailpit SMTP" "ports row from module"
  assert_contains "$(cat "$T/w/AGENTS.md")" "### Email (mail-catcher module)" "agent rule appended"
  grep -qx "/.playwright-mcp/" "$T/w/.gitignore" || fail "gitignore line from module"
  assert_fails "unknown module" "$KIT" add-module "$T/w" nope
}

t_check_reports_problems() {
  make_workspace
  "$KIT" check "$W" >"$T/out" 2>&1 || fail "fresh workspace has problems: $(cat "$T/out")"
  echo "# local" >> "$W/bin/ws"
  sed -i.bak 's/^name: jobs_devcontainer/name: other/' "$W/jobs/.devcontainer/compose.yaml" 2>/dev/null || true
  out="$("$KIT" check "$W" 2>&1)" && fail "check passed a broken workspace"
  assert_contains "$out" "core file differs from this kit: bin/ws" "core drift"
}

t_example_workspace_loads() {
  ex="$CANDIDATE/examples/rails-and-worker"
  # The example holds only owned files; give it the core the way kit init would.
  cp -R "$ex" "$T/ex" && cp -R "$CANDIDATE/core/." "$T/ex/"
  out="$(ROOT="$T/ex"; . "$T/ex/lib/ws/common.sh"; ws_load_conf; ws_load_resources; ws_projects; echo "$WS_LANE_LINKS")"
  assert_contains "$out" "app shop worktree 3200" "example projects parse"
  assert_contains "$out" "worker shop-worker shared -" "shared project parses"
  assert_contains "$out" "app:tmp/local_secret.txt" "example conf sources"
}

t_kit_usage() {
  assert_fails "no command" "$KIT"
  assert_contains "$(cat "$T/out")" "kit init <dir> --name <name>" "usage text"
  assert_contains "$("$KIT" modules)" "webhook-forward" "modules list"
}
