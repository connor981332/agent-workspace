# {{TITLE}} documentation

This workspace is the canonical home for product and technical documentation. Each fact has one home; link to it rather than keeping copies in project repos.

## Start here

| Question | Read |
|---|---|
| How do I develop and verify changes? | [Shared setup](development/setup.md) |
| How do the services talk to each other? | [Contracts](contracts/) |
<!-- Add product, architecture, design, and operations pages as they exist. -->

## Authority and status

Mark every page with a status line so agents can tell what binds them:

- **Agreed requirements** define intended behavior. An unimplemented requirement is work still to do, not evidence that a feature exists.
- **Drafts** preserve proposed designs and open questions. They are not approved specifications.
- **References** describe the current system or established constraints.
- **Contracts** between services are authoritative for both sides. Name the technical owner on each one.

If code conflicts with an agreed requirement or contract, investigate it as a bug or unfinished work. Change requirements deliberately and say why. Don't implement a draft because an older page looked authoritative.

## Access from project repositories

On the host, docs are at `../docs/` relative to each project root. Dev containers mount them read-only at `/{{NAME}}-workspace/docs/`, with the shared rules at `/{{NAME}}-workspace/AGENTS.md`. Edit central documents from the host workspace checkout.
