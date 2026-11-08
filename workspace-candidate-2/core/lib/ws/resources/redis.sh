# Lane resource: a Redis logical database per lane, numbered by its slot.
#
# The main checkout uses database 0; the lane in slot N gets
# <WS_REDIS_URL>/N in the lane variable WS_REDIS_ENV (REDIS_URL), so its
# queues, streams, jobs, and caches never meet another lane's. Redis has 16
# databases by default, so WS_LANE_SLOTS must stay below 16.
#
# Settings (workspace.conf):
#   WS_REDIS_URL      the URL containers use, without a database  (redis://redis:6379)
#   WS_REDIS_ENV      the variable the project reads              (REDIS_URL)
#   WS_REDIS_SERVICE  infra service running Redis, for drop       (redis)
#
# Redis has no cheap copy, so this adapter takes no snapshots. A restored
# Postgres snapshot can therefore point at jobs that no longer exist in Redis;
# workspaces that care add a lanes.d/setup step that clears or reseeds them.
#
# Part of the workspace core (`kit update` replaces it).

: "${WS_REDIS_URL:=redis://redis:6379}"
: "${WS_REDIS_ENV:=REDIS_URL}"
: "${WS_REDIS_SERVICE:=redis}"

res_redis_env() {
  [ "$2" -lt 16 ] || ws_die "slot $2 has no Redis database (Redis keeps 0-15); lower WS_LANE_SLOTS"
  echo "$WS_REDIS_ENV=$WS_REDIS_URL/$2"
}

res_redis_describe() { echo "$WS_REDIS_URL/$2"; }

res_redis_drop() {
  _id="$(ws_infra_container "$WS_REDIS_SERVICE")"
  [ -n "$_id" ] || { ws_warn "shared Redis is not running; database $2 was not flushed"; return 0; }
  docker exec "$_id" redis-cli -n "$2" FLUSHDB >/dev/null
}
