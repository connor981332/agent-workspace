# webhook-forward

> Forward a provider's sandbox webhooks to the main checkout or any lane, with keys read from a gitignored file so no password prompt interrupts a session.

**Status:** half-baked: one adapter (Stripe), and lanes are not isolated from each other's events.
**Origin:** fabworkspace `bin/stripe-listen`.

`bin/webhook-forward <provider> [lane]` finds the receiving project's port for the lane, runs the
adapter `lanes.d/webhooks/<provider>.sh` on the host, writes the signing secret to that checkout's
gitignored `.env.local`, and redacts secrets from the forwarder's output. Add a provider by writing an
adapter with `wh_prepare`, `wh_secret`, `wh_listen`, `WH_SECRET_VAR`, `WH_PATH`, and `WH_REDACT`.

## Why it exists

In fabworkspace, the first end-to-end run by hand kept stopping for password-manager approvals and
third-party onboarding. Keys moved to a gitignored file written once (`op inject`), every lane links
to it (`WS_LANE_LINKS`), and tools read it rather than asking again. Agents are denied reading the
file (`.claude/settings.json`), and scripts never print secrets.

## Contention (not solved)

- **One sandbox, many lanes.** Each forwarder receives *every* sandbox event, so two lanes forwarding
  at once both get both lanes' events. Apps must ignore events for objects they don't know.
- **Idempotency keys across lanes.** A lane restored from a snapshot reuses record IDs another lane
  already sent to the same sandbox. Keys built from IDs alone collided inside the provider's 24-hour
  window, so the provider refused the request or returned the other lane's object. Fix in the app: put
  something unique per record lifetime (its creation time) in the key.
- A per-lane provider account or sandbox would isolate both, at the cost of onboarding each one.
