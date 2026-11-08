# Resources and the queue

There are two kinds of sharing, and the design keeps them apart.

- **Lanes isolate files and data.** Every agent gets its own worktrees and its own provisioned data.
  Lanes are cheap and there can be many.
- **Resources ration scarce things.** A resource has a fixed capacity, and lanes take turns through a
  queue. The owner can always cut in.

Example: a Rails monolith with Webpack needs 12 GB of memory, so only one copy can run. Every agent
can have a lane and edit the monolith freely, but booting the full stack is a resource with capacity
1. Agents wait in line for it, and when the owner wants it, the agent holding it is evicted and the
owner's copy boots from the owner checkout.

## Declaring resources

```toml
# One copy of the monolith fits in memory.
[resources.rails]
kind = "process"
capacity = 1
servers = ["monolith.rails", "monolith.webpack"]   # started and stopped together
owner_counts = true          # the owner's own copy uses the seat
owner_preempts = true        # the owner cuts in line and evicts
evict_grace = "15s"          # SIGTERM to the process group, SIGKILL after this
max_hold = "45m"             # while others wait
idle_expiry = "20m"

[projects.monolith.servers.rails]
command = "exec bin/rails server -p {port}"
port = 3000
owner_port = 3000            # detects the owner's copy started without aw

[projects.monolith.servers.webpack]
command = "exec bin/webpack-dev-server --port {port}"
port = 3035

# Mobile devices are handles, not processes.
[resources.android]
kind = "token"
tokens = ["emulator-5554", "emulator-5556"]          # capacity is the number of tokens

[resources.ios-sim]
kind = "token"
capacity = 2
acquire = "scripts/sim-clone"     # extension protocol: returns the token, e.g. a simulator UDID
release = "scripts/sim-erase"

[commands.e2e]
project = "web"
run = "npx playwright test"
needs = ["web-stack"]
```

- A **process** resource owns servers. Holding a seat means `aw` has started those servers for the
  lane, in their own process group, on the seat's ports.
- A **token** resource hands out a handle. Holding a seat means the lane has exclusive use of that
  handle, exposed as `AW_TOKEN_<RESOURCE>`.
- Every server belongs to exactly one process resource. A server not listed in any resource belongs
  to the implicit resource `servers`, whose capacity is `[lanes] server_capacity` (default 2).
- Team commands declare `needs`. A command with no needs runs immediately. A command with needs
  queues, holds its seats while it runs and releases them when it exits.

## Seats and ports

Capacity counts concurrent holders, including the owner when `owner_counts = true`. The seat number
only chooses ports: the owner always uses seat 0, the base ports, and agents use seats 1 to
`capacity`. So the ports for seats 0 to `capacity` are the ones containers publish. A server in seat
`k` listens on `port + port_stride * k` (`port_stride` defaults to 100), and its URL uses the lane's
hostname, so browser cookies never collide across lanes even when ports are reused.

Ports are a function of the seat, not the lane, so the set of ports any lane might use is known in
advance and is published by every container when it is created (requirement R50).

## The queue

- Each resource has a first-in, first-out queue of waiting lanes.
- A lane holds at most one queue entry per resource.
- A request for several resources is granted atomically when all are available. Requests are
  evaluated in a fixed resource order, so two multi-resource requests cannot deadlock each other.
- A waiting agent sees its position (`aw serve rails` reports "position 2 in line for rails"). It can
  wait in the foreground (`--wait`) or return and be notified.
- A seat freed for a lane that is not waiting in the foreground is a reservation with a two-minute
  claim window. The agent is told on its next tool call and runs `aw serve` to start its servers, so
  the command that freed the seat never starts another lane's servers. An unclaimed reservation
  passes to the next lane. A `--wait` caller takes up its seat itself. A request that joins the queue
  exits with status 3.
- Notifications are attached to the agent's next tool call: granted, evicted, expiring soon, expired.
  The optional supervisor can also send desktop notifications to the human.

## Owner preemption

When the owner (from the owner checkout, or a pair session acting for the owner) asks for a resource
with `owner_preempts = true`:

1. The owner goes to the front of the queue.
2. If all seats are held, the lane holding the seat longest is evicted: its servers receive SIGTERM to
   their process group, then SIGKILL after `evict_grace`.
3. The evicted lane is told on its next tool call and rejoins the queue at the **head**.
4. The owner's servers start from the owner checkout, in seat 0, on the base ports.
5. When the owner stops (`aw stop rails`), or the tool sees the owner's server has gone, the seat
   passes to the next lane in line.

With `owner_preempts = false` the owner goes to the front of the queue but waits for the seat to be
released.

Eviction stops processes only. It never touches the lane's files, branches or data.

## Owner's own servers

The owner does not have to use `aw serve`. With `owner_counts = true`, a process listening on a
server's `owner_port` occupies seat 0, so agents cannot overcommit the machine while the owner runs
the app from an editor or terminal. Detection is a cheap local port check, done when the queue is
consulted.

## Time limits

- **Maximum hold.** When others are waiting and a lease has been held longer than `max_hold`, the
  holder is warned on its next tool call, then evicted after `evict_grace`. It rejoins the queue at
  the **tail**, because the limit exists for fairness.
- **Idle expiry.** A lease with no activity for `idle_expiry` is released. Activity is any of: the
  lane's tool calls, output in the server's log, or requests through the server (for servers that
  log them).

## Recovery

A lease records its holder's PID and process start time. When either no longer matches a live
process, the lease is dead: the next state-changing command frees the seat, stops any orphaned
processes in the recorded process group and grants the seat to the next lane. This is how a reboot,
a crashed harness or a killed terminal is recovered, with no daemon.

An unreadable lease record is treated as held, and `aw doctor` reports it with the fix.

## Why capacities are fixed

One memory budget shared by resources of different cost (a 28 GB pool where the monolith costs 12 and
the Next.js stack costs 2) was considered and rejected. Fixed capacities give the team exact control
over what can run at once. A pool would also be wrong on most machines, because a workspace cannot
know what else the developer runs: other workspaces, a blog, an editor. `aw doctor` warns when the
capacities of all workspaces registered on one machine could together need more memory than the
machine has, for resources that declare an approximate `memory`.
