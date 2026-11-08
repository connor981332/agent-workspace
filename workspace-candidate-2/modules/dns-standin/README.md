# dns-standin

> CoreDNS serving a fixture zone, so code that reads real DNS (custom domains, verification records) runs against real answers in development.

**Status:** half-baked: one zone, edited by hand. **Origin:** fabworkspace `infra/dns/`, built for
customer custom domains (CNAME to a target, TXT ownership proofs, lapse and transfer).

Adds `infra/compose.d/dns.yaml`, `infra/dns/Corefile`, and `infra/dns/{{NAME}}-dev.test.zone`. It answers
only for `{{NAME}}-dev.test`, on the shared network as `dns:53`; it is not the machine's resolver.
Projects point their resolver at it by configuration (for example `DNS_NAMESERVERS=dns`).

The zone keeps one record set per scenario (right setup, wrong token, missing record, pointing
elsewhere, flattened apex, vanished), with fixed tokens that tests set on their records. `$TTL 5`
keeps edits visible quickly; CoreDNS reloads the file on change.

Lanes share the zone. Scenarios that need a name per run must create unique names in tests, or the
zone needs per-lane subdomains.
