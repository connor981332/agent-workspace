# Test helpers. POSIX sh. Each test runs in its own temporary directory with the
# docker and devcontainer stubs first on PATH.

TESTS="$(pwd -P)"   # run.sh cds into tests/ first
CANDIDATE="$(dirname "$TESTS")"
KIT="$CANDIDATE/kit"

fail() { echo "    FAIL: $*" >&2; touch "$T/.failed"; }

assert_eq() { # <expected> <actual> <what>
  [ "$1" = "$2" ] || fail "$3: expected [$1], got [$2]"
}
assert_contains() { # <haystack> <needle> <what>
  case "$1" in *"$2"*) ;; *) fail "$3: [$2] not found in [$1]" ;; esac
}
assert_not_contains() {
  case "$1" in *"$2"*) fail "$3: [$2] unexpectedly in [$1]" ;; esac
}
assert_file() { [ -e "$1" ] || fail "missing $1"; }
assert_no_file() { [ ! -e "$1" ] && [ ! -L "$1" ] || fail "unexpected $1"; }
assert_fails() { # <what> <command...>: the command must exit non-zero
  _what="$1"; shift
  if "$@" >"$T/out" 2>&1; then fail "$_what: succeeded but should fail"; fi
}
log() { cat "$STUB_LOG" 2>/dev/null || true; }

setup_test() {
  T="$(mktemp -d "${TMPDIR:-/tmp}/c2test.XXXXXX")"
  T="$(cd "$T" && pwd -P)"
  STUB_LOG="$T/stub.log"; STUB_STATE="$T/stub"
  mkdir -p "$STUB_STATE"
  : > "$STUB_LOG"
  export STUB_LOG STUB_STATE
  PATH="$TESTS/stubs:$PATH"; export PATH
  export GIT_CONFIG_NOSYSTEM=1
  export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
}

teardown_test() { rm -rf "$T"; }

# A remote repo with one commit on main, and a clone of it at <dest>.
make_repo() { # <name> <dest> [file content-of-config]
  git init -q --bare -b main "$T/remotes/$1.git" 2>/dev/null ||
    { git init -q --bare "$T/remotes/$1.git"; git -C "$T/remotes/$1.git" symbolic-ref HEAD refs/heads/main; }
  git clone -q "$T/remotes/$1.git" "$T/seed-$1" 2>/dev/null
  git -C "$T/seed-$1" checkout -q -b main 2>/dev/null || true
  mkdir -p "$T/seed-$1/config"
  echo "${3:-database: x}" > "$T/seed-$1/config/database.yml"
  git -C "$T/seed-$1" add -A && git -C "$T/seed-$1" commit -qm init && git -C "$T/seed-$1" push -q origin main
  git clone -q "$T/remotes/$1.git" "$2"
}

# A workspace W with project "app" (repo web, lane worktrees, port 3000) and
# "worker" (repo jobs, shared lanes, no port), both cloned, stubs configured.
make_workspace() {
  W="$T/ws"
  "$KIT" init "$W" --name shop --remote "$T/remotes" >/dev/null
  make_repo web "$W/web" "database: shop_development<%= ENV['WS_LANE'] %>"
  make_repo jobs "$W/jobs"
  "$KIT" add-project "$W" app web --lanes worktree --port 3000 >/dev/null
  "$KIT" add-project "$W" worker jobs >/dev/null
  cat >> "$W/workspace.conf" <<CONF
WS_DEVCONTAINER_CLI="$TESTS/stubs/devcontainer"
WS_LANE_GUARDS="app:config/database.yml:WS_LANE"
WS_LANE_LINKS="app:.env.development.local app:config/master.key"
CONF
  echo "SECRET=1" > "$W/web/.env.development.local"
  echo "key" > "$W/web/config/master.key"
  # Shared infrastructure is "running".
  mkdir -p "$STUB_STATE/ps" "$STUB_STATE/mount" "$STUB_STATE/state"
  echo pg1 > "$STUB_STATE/ps/shop-infra__postgres"
  echo rd1 > "$STUB_STATE/ps/shop-infra__redis"
}
