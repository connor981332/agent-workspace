# plans

> A plan index with statuses, and a verification practice: automated acceptance on snapshot fixtures plus a short owner checklist, so the owner's review waits in a lane while the next work starts.

**Status:** working practice, mostly documentation. **Origin:** fabworkspace `plans/` and its
roadmap's delivery rules.

Adds `plans/README.md` (index and statuses) and `plans/_template/` (`README.md`, `STATUS.md`).

## The practice, and why lanes need it

1. **Done-when checks are code**: tests with fakes, plus live acceptance tasks that run against real
   provider sandboxes from a lane with no browser.
2. **Persistent fixtures in snapshots.** Slow or human-gated setup (onboarding test accounts with
   payment and shipping providers) is done once, then kept as a lane snapshot (`bin/lane snapshot`).
   Each regression run starts with `bin/lane restore <lane> <snapshot>`. A snapshot older than the
   code needs the project's migrations after restoring.
3. **An owner checklist** holds only what needs a person. It waits in the work's lane while the
   next milestone is built in another lane, which is why lanes are held until the checklist is done.
