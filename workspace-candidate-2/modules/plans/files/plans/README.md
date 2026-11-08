# Plans

Cross-project plans, research, and handoffs. This index is the source of truth for every plan's status.

Each plan gets its own directory with a `README.md`; larger plans add a `STATUS.md` for stage-by-stage progress. Start from `_template/`.

## How a plan is verified

- **Done-when checks are code.** Tests with fakes for every stage, and live acceptance tasks for flows that cross real providers. Write them as stages land, not at the end.
- **Persistent test accounts** are onboarded once and kept as a lane snapshot (`bin/lane snapshot`); runs restore it first.
- **An owner checklist** in the status file lists only what needs a person, so it can wait in the plan's lane while the next plan starts in another.

## Statuses

| Status | Meaning |
|---|---|
| Draft | Being written or researched. Not ready to build. |
| Ready | Agreed and ready to carry out. |
| In progress | Being built. The status file says which stages have landed. |
| Done | Shipped and verified. |
| Shelved | Paused on purpose. The plan says why. |

## Index

| Plan | Status | Summary |
|---|---|---|
