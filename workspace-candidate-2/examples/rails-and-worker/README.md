# Example: a Rails app and a queue worker

The shape fabworkspace had, with neutral names: a Rails app (`app`, repo `shop`) that gets a worktree
per lane, and a worker (`worker`, repo `shop-worker`) that consumes jobs from Redis and runs from its
main checkout in every lane, on the lane's Redis database. These are the owned files that differ from
a fresh `kit init --name shop`. The rest is generated.

The app's half of the bargain, in `config/database.yml`:

```erb
<% suffix = ENV["WS_LANE"].to_s.empty? ? "" : "_#{ENV["WS_LANE"]}" %>
development:
  primary:
    database: shop_development<%= suffix %>
  cable:
    database: shop_development_cable<%= suffix %>
test:
  database: shop_test<%= suffix %>
```

Typical use:

```sh
bin/lane new m4 m4/threads          # worktree shop/.worktrees/m4, slot 1, db:prepare via lanes.d/setup
bin/ws app@m4 bin/dev               # http://localhost:3201
bin/ws worker@m4 npm run dev        # the main worker checkout on redis db 1
bin/lane restore m4 onboarded       # persistent test accounts, then:
bin/ws app@m4 bin/rails db:migrate  # snapshots lag the code
bin/ws app@m4 bin/rails acceptance:m4
```
