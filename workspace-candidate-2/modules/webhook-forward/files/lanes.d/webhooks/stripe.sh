# Stripe adapter for bin/webhook-forward (webhook-forward module).
#
# Reads STRIPE_SECRET_KEY from the main checkout's WS_STRIPE_ENV_FILE and
# refuses anything but a sandbox key. WS_STRIPE_FORWARD is "account" (events on
# your own account), "connect" (connected accounts' events), or "both".

: "${WS_STRIPE_ENV_FILE:=.env.development.local}"
: "${WS_STRIPE_FORWARD:=account}"
WH_SECRET_VAR=STRIPE_WEBHOOK_SECRET
WH_PATH="${WS_STRIPE_PATH:-/webhooks/stripe}"
WH_REDACT='s/whsec_[A-Za-z0-9]+/whsec_…/g'

wh_prepare() {
  command -v stripe >/dev/null || ws_die "install the Stripe CLI on the host"
  env_file="$1/$WS_STRIPE_ENV_FILE"
  [ -f "$env_file" ] || ws_die "$env_file is missing; write the sandbox keys there first"
  STRIPE_API_KEY="$(sed -n 's/^STRIPE_SECRET_KEY=//p' "$env_file" | tr -d "\"'")"
  case "$STRIPE_API_KEY" in
    sk_test_*) ;;
    *) ws_die "STRIPE_SECRET_KEY must be a sandbox key (sk_test_)" ;;
  esac
  export STRIPE_API_KEY
}

wh_secret() { stripe listen --print-secret --skip-update; }

wh_listen() {
  case "$WS_STRIPE_FORWARD" in
    account) set -- --forward-to "$1" ;;
    connect) set -- --forward-connect-to "$1" ;;
    both) set -- --forward-to "$1" --forward-connect-to "$1" ;;
    *) ws_die "WS_STRIPE_FORWARD is account, connect, or both" ;;
  esac
  stripe listen --all-snapshot --skip-update "$@"
}
