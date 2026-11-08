# Lane resource: Postgres databases on the shared server, one set per lane.
#
# The lane's databases are the names in WS_PG_DATABASES with _<lane> appended.
# The project appends the suffix itself from the lane variable (WS_LANE, or
# WS_LANE_VAR), for example in Rails' config/database.yml:
#
#   <% suffix = ENV["WS_LANE"].to_s.empty? ? "" : "_#{ENV["WS_LANE"]}" %>
#   database: shop_development<%= suffix %>
#
# Snapshots are databases named <WS_PG_SNAPSHOT_PREFIX><snapshot>, copied
# server-side with CREATE DATABASE ... TEMPLATE, so they need no dump tools.
# Postgres refuses to copy a database that has connections, so stop the lane's
# servers and consumers before a snapshot or restore.
#
# Settings (workspace.conf):
#   WS_PG_SERVICE      infra service running Postgres        (postgres)
#   WS_PG_USER         superuser for admin statements        (postgres)
#   WS_PG_DATABASES    the main checkout's database names    (required)
#   WS_PG_SNAPSHOT     which of them snapshots copy          (the first)
#   WS_PG_SNAPSHOT_PREFIX                                    (<WS_NAME>_snapshot_)
#   WS_PG_CREATE       1: create empty databases at lane new (0: the project's
#                      setup creates them, for example with db:prepare)
#   WS_PG_TERMINATE    1: end other connections before restore and drop (0)
#
# Adapters share the caller's variables (sh has no locals), so every variable
# here starts with _pg_. bin/lane also runs each hook in a subshell.
#
# Part of the workspace core (`kit update` replaces it).

: "${WS_PG_SERVICE:=postgres}"
: "${WS_PG_USER:=postgres}"
: "${WS_PG_DATABASES:=}"
: "${WS_PG_SNAPSHOT:=}"
: "${WS_PG_SNAPSHOT_PREFIX:=${WS_NAME}_snapshot_}"
: "${WS_PG_CREATE:=0}"
: "${WS_PG_TERMINATE:=0}"

_pg_first() { set -- $WS_PG_DATABASES; echo "${1:-}"; }

_pg_container() {
  [ -n "$WS_PG_DATABASES" ] || ws_die "the postgres lane resource needs WS_PG_DATABASES in workspace.conf"
  _id="$(ws_infra_container "$WS_PG_SERVICE")"
  [ -n "$_id" ] || ws_die "shared Postgres ($WS_INFRA_PROJECT $WS_PG_SERVICE) is not running; start a project container or run bin/ws infra up -d"
  echo "$_id"
}

# _pg <sql>: one statement against the maintenance database.
_pg() {
  docker exec -i "$(_pg_container)" psql -U "$WS_PG_USER" -d postgres -v ON_ERROR_STOP=1 -qAt -c "$1"
}

_pg_terminate() { # <db>
  [ "$WS_PG_TERMINATE" = 1 ] || return 0
  _pg "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$1' AND pid <> pg_backend_pid()" >/dev/null
}

res_postgres_describe() {
  _out=""
  for _pg_db in $WS_PG_DATABASES; do _out="$_out ${_pg_db}_$1"; done
  echo "databases:$_out"
}

res_postgres_create() {
  [ "$WS_PG_CREATE" = 1 ] || return 0
  for _pg_db in $WS_PG_DATABASES; do
    if [ -z "$(_pg "SELECT 1 FROM pg_database WHERE datname = '${_pg_db}_$1'")" ]; then
      _pg "CREATE DATABASE \"${_pg_db}_$1\""
    fi
  done
}

res_postgres_drop() {
  # Parallel test runners add numbered copies (<db>_<lane>_0, _1, ...).
  for _pg_db in $WS_PG_DATABASES; do
    for _pg_name in "${_pg_db}_$1" $(_pg "SELECT datname FROM pg_database WHERE datname ~ '^${_pg_db}_$1_[0-9]+\$'"); do
      _pg_terminate "$_pg_name"
      _pg "DROP DATABASE IF EXISTS \"$_pg_name\""
    done
  done
}

res_postgres_snapshot() {
  _pg_src="${WS_PG_SNAPSHOT:-$(_pg_first)}_$1"
  _pg "CREATE DATABASE \"${WS_PG_SNAPSHOT_PREFIX}$2\" TEMPLATE \"$_pg_src\"" ||
    ws_die "could not copy $_pg_src; stop the lane's servers and consumers (Postgres can't copy a database in use)"
}

res_postgres_restore() {
  _pg_snap="${WS_PG_SNAPSHOT_PREFIX}$2"
  [ -n "$(_pg "SELECT 1 FROM pg_database WHERE datname = '$_pg_snap'")" ] || ws_die "no snapshot '$2' (bin/lane snapshots)"
  _pg_dst="${WS_PG_SNAPSHOT:-$(_pg_first)}_$1"
  _pg_terminate "$_pg_dst"
  _pg "DROP DATABASE IF EXISTS \"$_pg_dst\"" ||
    ws_die "could not drop $_pg_dst; stop the lane's servers and consumers first"
  _pg "CREATE DATABASE \"$_pg_dst\" TEMPLATE \"$_pg_snap\""
}

res_postgres_snapshots() {
  _pg "SELECT substr(datname, length('$WS_PG_SNAPSHOT_PREFIX') + 1) FROM pg_database WHERE starts_with(datname, '$WS_PG_SNAPSHOT_PREFIX') ORDER BY 1"
}
