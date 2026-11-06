# Plans (optional)

Long work outlives a single session, so a plan keeps its state where the next agent will look. If you
do not create a `plans/` directory, none of this applies.

- One file per plan: `plans/<slug>.md`, with a row in `plans/README.md`.
- The first `## Status` block has these fields: `**State:**`, `**Updated:**` (YYYY-MM-DD),
  `**Branches / MRs:**`, `**Done:**`, `**In progress:**` and `**Next step:**`.
- State is one of `DRAFT`, `PLANNED`, `PARTIALLY IMPLEMENTED`, `FULLY IMPLEMENTED` or `FULLY VERIFIED`.
  The index row must agree with it.
- Never use a separate `STATUS.md`.

| Command | What it does |
|---|---|
| `bin/aw plans [<slug>]` | Show where plans stand, or one plan's full Status. |
| `bin/aw check-docs` | Check the conventions above and relative Markdown links. |
| `bin/aw plan-reminder [<repo>]` | Warn when a plan listing this branch looks stale. Suitable for a pre-commit hook. |

If `.githooks/` exists, `bin/aw bootstrap` points every repository's `core.hooksPath` at it.
