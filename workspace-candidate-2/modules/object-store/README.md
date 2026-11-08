# object-store

> Garage, a single-node S3-compatible store, with a bucket, a development key, and browser CORS.

**Status:** half-baked: lanes share one bucket. **Origin:** fabworkspace `infra/compose.yaml` (`garage`,
`garage-cors`) and `infra/garage.toml`, which replaced MinIO to match production.

Adds `infra/compose.d/object-store.yaml` and `infra/object-store/garage.toml`. Containers use
`http://garage:3900`, region `garage`, bucket `{{NAME}}-uploads`, and the fixed development key in the
Compose file. Browsers upload to the published port with presigned URLs. A one-shot `garage-cors`
job sets CORS on every infrastructure start; it is not in `WS_INFRA_SERVICES` because it exits.

## Findings carried over

- Garage matches CORS header names case-sensitively and browsers send lowercase names in the
  preflight, so the rule lists lowercase headers.
- The bucket appears a moment after Garage reports healthy, so the CORS job retries.
- The image has no shell; the health check is `garage status`.

## Contention (not solved)

Lanes share the bucket. A database snapshot restored into two lanes points both at the same objects,
so a lane-wide purge or retention job in one lane can delete files the other lane, or the snapshot,
still uses. In fabworkspace this was handled by convention: checks purged single records, never the
whole lane. A `bucket` lane resource (a bucket or key prefix per lane, copied on snapshot) would fix it
and is the obvious next step.
