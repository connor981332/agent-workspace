# mail-catcher

> Mailpit catches all development email; nothing reaches real recipients.

**Status:** working, but lanes share it. **Origin:** fabworkspace `infra/compose.yaml` (`mailpit`).

Adds `infra/compose.d/mailpit.yaml`. Containers send SMTP to `mailpit:1025`; the inbox is on the host
at the web UI port.

## Contention

Every lane and the main checkout share one inbox. In fabworkspace, an acceptance check that looked for
"the summary email" found an earlier run's identical message and failed. Checks in lanes must count
what *they* added (or match on lane-unique content), not assume an empty inbox. A per-lane inbox would
need a Mailpit per lane or tagging (Mailpit supports `X-Tags` headers); neither is built here.
