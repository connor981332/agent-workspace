// Package broker rations scarce things among lanes (resources.md): seats, a first-in, first-out
// queue, owner preemption, recovery of dead holders and the notifications that go with them. There
// is no daemon: the queue moves forward in the transaction of whichever command frees a seat.
package broker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/procs"
	"github.com/connor981332/agent-workspace/internal/state"
)

// ClaimWindow is how long a lane has to take up a seat it was granted while it was not waiting (S2).
const ClaimWindow = 2 * time.Minute

// Holder is who is asking. Lane 0 is the owner.
type Holder struct {
	Lane       int
	Generation int
	Name       string
	PID        int
	Start      string
}

// Broker is the queue of one workspace.
type Broker struct {
	Store *state.Store
	Cfg   *config.Config
	// ReleaseToken runs a token resource's release script. A failure while a lane gives a seat back
	// keeps the lease, so that the release can be retried.
	ReleaseToken func(ctx context.Context, r Resource, l state.Lease) error
	// ListeningIn reports whether something listens on a port in a project's container. On the host
	// Docker's proxy holds a published port whether or not anything does.
	ListeningIn func(project string, port int) bool
}

// Resource is a resource as the queue sees it, including the implicit "servers".
type Resource struct {
	Name          string
	Kind          string
	Capacity      int
	Servers       []string // <project>.<server>
	Tokens        []string
	OwnerCounts   bool
	OwnerPreempts bool
	EvictGrace    time.Duration
	MaxHold       time.Duration
	IdleExpiry    time.Duration
	Acquire       string
	Release       string
}

// Resources are every resource, in the fixed order requests are evaluated in.
func (b *Broker) Resources() []Resource {
	listed := map[string]bool{}
	var out []Resource
	for name, r := range b.Cfg.Resources {
		for _, s := range r.Servers {
			listed[s] = true
		}
		capacity := r.Capacity
		if r.Kind == "token" && len(r.Tokens) > 0 {
			capacity = len(r.Tokens)
		}
		out = append(out, Resource{Name: name, Kind: r.Kind, Capacity: capacity, Servers: r.Servers, Tokens: r.Tokens,
			OwnerCounts: r.OwnerCounts, OwnerPreempts: r.OwnerPreempts, EvictGrace: time.Duration(r.EvictGrace),
			MaxHold: time.Duration(r.MaxHold), IdleExpiry: time.Duration(r.IdleExpiry), Acquire: r.Acquire, Release: r.Release})
	}
	var implicit []string
	for pname, p := range b.Cfg.Projects {
		for sname := range p.Servers {
			if full := pname + "." + sname; !listed[full] {
				implicit = append(implicit, full)
			}
		}
	}
	if len(implicit) > 0 {
		slices.Sort(implicit)
		out = append(out, Resource{Name: "servers", Kind: "process", Capacity: b.Cfg.Lanes.ServerCapacity, Servers: implicit,
			OwnerCounts: true, OwnerPreempts: true, EvictGrace: 15 * time.Second})
	}
	slices.SortFunc(out, func(a, b Resource) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Lookup finds a resource by name.
func (b *Broker) Lookup(name string) (Resource, error) {
	var names []string
	for _, r := range b.Resources() {
		if r.Name == name {
			return r, nil
		}
		names = append(names, r.Name)
	}
	return Resource{}, fmt.Errorf("there is no resource named %q → known: %s", name, strings.Join(names, ", "))
}

// server returns a server's configuration from <project>.<server>.
func (b *Broker) server(full string) *config.Server {
	project, name, _ := strings.Cut(full, ".")
	if p := b.Cfg.Projects[project]; p != nil {
		return p.Servers[name]
	}
	return nil
}

// ownerPortBusy reports whether the owner's own copy of any of a resource's servers is listening: a
// cheap local connection, made before any transaction starts.
func (b *Broker) ownerPortBusy(r Resource) bool {
	if !r.OwnerCounts {
		return false
	}
	for _, full := range r.Servers {
		s := b.server(full)
		if s == nil || s.OwnerPort == 0 {
			continue
		}
		port := s.OwnerPort + b.Cfg.Ports.Offset
		if project, _, _ := strings.Cut(full, "."); b.Cfg.Projects[project].Runner == "devcontainer" && b.ListeningIn != nil {
			if b.ListeningIn(project, port) {
				return true
			}
			continue
		}
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 20*time.Millisecond)
		if err == nil {
			c.Close()
			return true
		}
	}
	return false
}

// Outcome is the result of a request.
type Outcome struct {
	Granted   bool
	Leases    []state.Lease
	Positions map[string]int // when queued: the place in line for each resource, from 1
	Evicted   []Evicted      // lanes the owner's request displaced
}

// Evicted is a displaced lease, whose processes the caller stops outside the transaction.
type Evicted struct {
	Resource string
	Lane     string
	Grace    time.Duration
	Procs    []procs.Proc
	Token    func(context.Context) // releases the seat's token, if it has a script to
}

type snapshot struct {
	byName    map[string]Resource
	ownerBusy map[string]bool
}

func (b *Broker) snapshot() snapshot {
	s := snapshot{byName: map[string]Resource{}, ownerBusy: map[string]bool{}}
	for _, r := range b.Resources() {
		s.byName[r.Name] = r
		s.ownerBusy[r.Name] = b.ownerPortBusy(r)
	}
	return s
}

// freeSeat returns the seat a holder would get, or -1. Capacity counts holders, including the owner
// when owner_counts is on. The owner always uses seat 0, and agents use seats 1 to capacity (S1).
func freeSeat(r Resource, snap snapshot, leases []state.Lease, lane int) int {
	held := 0
	ownerIn := false
	taken := map[int]bool{}
	for _, l := range leases {
		if l.Resource != r.Name {
			continue
		}
		taken[l.Seat] = true
		if l.Lane == 0 {
			ownerIn = true
			if r.OwnerCounts {
				held++
			}
		} else {
			held++
		}
	}
	if !ownerIn && snap.ownerBusy[r.Name] {
		held++
	}
	if lane == 0 {
		if ownerIn || (r.OwnerCounts && held >= r.Capacity) {
			return -1
		}
		return 0
	}
	if held >= r.Capacity {
		return -1
	}
	for seat := 1; seat <= r.Capacity; seat++ {
		if !taken[seat] {
			return seat
		}
	}
	return -1
}

func token(r Resource, leases []state.Lease) string {
	for _, t := range r.Tokens {
		if !slices.ContainsFunc(leases, func(l state.Lease) bool { return l.Resource == r.Name && l.Token == t }) {
			return t
		}
	}
	return ""
}

// advance grants seats to whoever is next. A request is granted whole or not at all (R45), and it
// holds back the requests behind it on each of its resources, so nobody is overtaken.
func (b *Broker) advance(tx *sql.Tx, snap snapshot, now time.Time) ([]state.Lease, error) {
	queue, err := state.QueueEntries(tx)
	if err != nil {
		return nil, err
	}
	leases, err := state.Leases(tx)
	if err != nil {
		return nil, err
	}
	var granted []state.Lease
	blocked := map[string]bool{}
	done := map[string]bool{}
	for _, e := range queue {
		if done[e.Request] {
			continue
		}
		done[e.Request] = true
		var names []string
		for _, o := range queue {
			if o.Request == e.Request {
				names = append(names, o.Resource)
			}
		}
		slices.Sort(names)
		fits := true
		seats := map[string]int{}
		for _, name := range names {
			r, known := snap.byName[name]
			if !known {
				fits = false
				break
			}
			seat := freeSeat(r, snap, leases, e.Lane)
			if blocked[name] || seat < 0 {
				fits = false
				break
			}
			seats[name] = seat
		}
		if !fits {
			for _, name := range names {
				blocked[name] = true
			}
			continue
		}
		for _, name := range names {
			r := snap.byName[name]
			l := state.Lease{Resource: name, Lane: e.Lane, Generation: e.Generation, LaneName: e.LaneName, Seat: seats[name], State: state.LeaseReserved,
				Token: token(r, leases), Granted: stamp(now), ClaimBy: stamp(now.Add(ClaimWindow)), LastActivity: stamp(now)}
			id, err := state.InsertLease(tx, l)
			if err != nil {
				return nil, err
			}
			l.ID = id
			leases = append(leases, l)
			granted = append(granted, l)
		}
		if err := state.RemoveRequest(tx, e.Request); err != nil {
			return nil, err
		}
	}
	return granted, nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parse(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// notifyGrants tells lanes that were given a seat they did not wait for.
func notifyGrants(tx *sql.Tx, granted []state.Lease, except int) error {
	for _, l := range granted {
		if l.Lane == 0 || l.Lane == except {
			continue
		}
		if err := state.AddNotification(tx, l.Lane, "granted", fmt.Sprintf("A seat on %s is reserved for this lane → run bin/aw serve %s within %s, or it passes to the next in line", l.Resource, l.Resource, ClaimWindow)); err != nil {
			return err
		}
	}
	return nil
}

// Request asks for resources: it returns leases if they are free, or the place in line. The owner
// goes to the front, and evicts the longest holder where the resource allows it (R39).
func (b *Broker) Request(ctx context.Context, h Holder, names []string) (*Outcome, error) {
	names = slices.Clone(names)
	slices.Sort(names)
	snap := b.snapshot()
	out := &Outcome{Positions: map[string]int{}}
	err := b.Store.Write(ctx, func(tx *sql.Tx) error {
		*out = Outcome{Positions: map[string]int{}}
		now := time.Now()
		limited, err := b.expire(tx, snap, now)
		if err != nil {
			return err
		}
		out.Evicted = limited
		leases, err := state.Leases(tx)
		if err != nil {
			return err
		}
		mine := func() []state.Lease {
			var got []state.Lease
			for _, l := range leases {
				if l.Lane == h.Lane && slices.Contains(names, l.Resource) {
					got = append(got, l)
				}
			}
			return got
		}
		if len(mine()) < len(names) {
			if err := b.enqueue(tx, h, names, mine()); err != nil {
				return err
			}
			if h.Lane == 0 {
				if err := b.preempt(tx, snap, names, leases, now, out); err != nil {
					return err
				}
			}
			granted, err := b.advance(tx, snap, now)
			if err != nil {
				return err
			}
			if err := notifyGrants(tx, granted, h.Lane); err != nil {
				return err
			}
			if leases, err = state.Leases(tx); err != nil {
				return err
			}
		}
		got := mine()
		if len(got) == len(names) {
			out.Granted = true
			for i := range got {
				got[i].State, got[i].PID, got[i].PIDStart = state.LeaseHeld, h.PID, h.Start
				got[i].LastActivity = stamp(now)
				if err := state.UpdateLease(tx, got[i]); err != nil {
					return err
				}
			}
			out.Leases = got
			return nil
		}
		queue, err := state.QueueEntries(tx)
		if err != nil {
			return err
		}
		for _, name := range names {
			pos := 0
			for _, e := range queue {
				if e.Resource != name {
					continue
				}
				pos++
				if e.Lane == h.Lane {
					out.Positions[name] = pos
				}
			}
		}
		return nil
	})
	return out, err
}

func (b *Broker) enqueue(tx *sql.Tx, h Holder, names []string, have []state.Lease) error {
	queue, err := state.QueueEntries(tx)
	if err != nil {
		return err
	}
	request := fmt.Sprintf("%d-%d", h.Lane, time.Now().UnixNano())
	priority := state.QueueNormal
	if h.Lane == 0 {
		priority = state.QueueOwner
	}
	for _, e := range queue {
		if e.Lane == h.Lane && slices.Contains(names, e.Resource) {
			return nil // already in line, with its place kept
		}
	}
	for _, name := range names {
		if slices.ContainsFunc(have, func(l state.Lease) bool { return l.Resource == name }) {
			continue
		}
		if err := state.Enqueue(tx, state.QueueEntry{Request: request, Resource: name, Lane: h.Lane, Generation: h.Generation, LaneName: h.Name, Priority: priority}); err != nil {
			return err
		}
	}
	return nil
}

// preempt evicts, for each full resource the owner may take, the lane that has held a seat longest.
func (b *Broker) preempt(tx *sql.Tx, snap snapshot, names []string, leases []state.Lease, now time.Time, out *Outcome) error {
	for _, name := range names {
		r := snap.byName[name]
		if !r.OwnerPreempts || freeSeat(r, snap, leases, 0) >= 0 {
			continue
		}
		var victim *state.Lease
		for i, l := range leases {
			if l.Resource == name && l.Lane != 0 && (victim == nil || parse(l.Granted).Before(parse(victim.Granted))) {
				victim = &leases[i]
			}
		}
		if victim == nil {
			continue // the seat is the owner's own copy, started without aw
		}
		ev, err := b.evict(tx, r, *victim, state.QueueEvicted, fmt.Sprintf("Your seat on %s was given to the owner, and its servers are being stopped. This lane is first in line for the next seat. Files, branches and data are untouched.", name))
		if err != nil {
			return err
		}
		out.Evicted = append(out.Evicted, ev)
		leases = slices.DeleteFunc(leases, func(l state.Lease) bool { return l.ID == victim.ID })
	}
	return nil
}

// evict removes a lease and puts its lane back in the queue. Only its processes are stopped (R40).
func (b *Broker) evict(tx *sql.Tx, r Resource, l state.Lease, priority int, message string) (Evicted, error) {
	ps, err := state.LeaseProcs(tx, l.ID)
	if err != nil {
		return Evicted{}, err
	}
	if err := state.DeleteLease(tx, l.ID); err != nil {
		return Evicted{}, err
	}
	if err := state.Enqueue(tx, state.QueueEntry{Request: fmt.Sprintf("evicted-%d", l.ID), Resource: r.Name, Lane: l.Lane, Generation: l.Generation, LaneName: l.LaneName, Priority: priority}); err != nil {
		return Evicted{}, err
	}
	if err := state.AddNotification(tx, l.Lane, "evicted", message); err != nil {
		return Evicted{}, err
	}
	return Evicted{Resource: r.Name, Lane: l.LaneName, Grace: r.EvictGrace, Procs: toProcs(ps), Token: b.tokenRelease(r, l)}, nil
}

func toProcs(ps []state.LeaseProc) []procs.Proc {
	out := make([]procs.Proc, 0, len(ps))
	for _, p := range ps {
		out = append(out, procs.Proc{PID: p.PID, Start: p.Start, Log: p.Log})
	}
	return out
}

// lastActive is the later of a lease's recorded activity and its servers' output (resources.md).
func lastActive(l state.Lease, ps []state.LeaseProc) time.Time {
	last := parse(l.LastActivity)
	for _, p := range ps {
		if info, err := os.Stat(p.Log); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return last
}

// expire applies the time limits: an unclaimed reservation passes on, a holder over its maximum
// hold while others wait is warned and then sent to the tail of the queue, and an idle lease is let
// go. It returns the holders displaced, whose processes the caller stops outside the transaction.
func (b *Broker) expire(tx *sql.Tx, snap snapshot, now time.Time) ([]Evicted, error) {
	leases, err := state.Leases(tx)
	if err != nil {
		return nil, err
	}
	queue, err := state.QueueEntries(tx)
	if err != nil {
		return nil, err
	}
	var out []Evicted
	changed := false
	for _, l := range leases {
		r, known := snap.byName[l.Resource]
		if !known {
			continue
		}
		if l.State == state.LeaseReserved {
			if l.ClaimBy != "" && now.After(parse(l.ClaimBy)) {
				if err := state.DeleteLease(tx, l.ID); err != nil {
					return nil, err
				}
				if err := state.AddNotification(tx, l.Lane, "expired", fmt.Sprintf("The seat reserved on %s was not taken up in time, and has passed on → run bin/aw serve %s to queue again", l.Resource, l.Resource)); err != nil {
					return nil, err
				}
				changed = true
			}
			continue
		}
		if l.Lane == 0 {
			continue // the owner's seat has no limits: the owner always wins
		}
		ps, err := state.LeaseProcs(tx, l.ID)
		if err != nil {
			return nil, err
		}
		if r.IdleExpiry > 0 && now.Sub(lastActive(l, ps)) > r.IdleExpiry {
			if err := state.DeleteLease(tx, l.ID); err != nil {
				return nil, err
			}
			if err := state.AddNotification(tx, l.Lane, "expired", fmt.Sprintf("The seat on %s was idle for %s, so it was released and its servers stopped → run bin/aw serve %s to take a seat again", l.Resource, r.IdleExpiry, l.Resource)); err != nil {
				return nil, err
			}
			out = append(out, Evicted{Resource: r.Name, Lane: l.LaneName, Grace: r.EvictGrace, Procs: toProcs(ps), Token: b.tokenRelease(r, l)})
			changed = true
			continue
		}
		waiting := slices.ContainsFunc(queue, func(e state.QueueEntry) bool { return e.Resource == l.Resource && e.Lane != l.Lane })
		if r.MaxHold <= 0 || !waiting || now.Sub(parse(l.Granted)) <= r.MaxHold {
			continue
		}
		if l.Warned == "" {
			l.Warned = stamp(now)
			if err := state.UpdateLease(tx, l); err != nil {
				return nil, err
			}
			if err := state.AddNotification(tx, l.Lane, "expiring", fmt.Sprintf("Others are waiting for %s and this lane has held it for over %s: the seat will be taken in %s → finish up, or run bin/aw stop %s", l.Resource, r.MaxHold, r.EvictGrace, l.Resource)); err != nil {
				return nil, err
			}
			continue
		}
		if now.Sub(parse(l.Warned)) > r.EvictGrace {
			ev, err := b.evict(tx, r, l, state.QueueNormal, fmt.Sprintf("This lane held %s for over %s while others waited, so its seat was given up and its servers are being stopped. It is back in line, at the end. Files, branches and data are untouched.", l.Resource, r.MaxHold))
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
			changed = true
		}
	}
	if !changed {
		return out, nil
	}
	granted, err := b.advance(tx, snap, now)
	if err != nil {
		return nil, err
	}
	return out, notifyGrants(tx, granted, -1)
}

// tokenRelease returns the best-effort release of a seat that has already been taken away.
func (b *Broker) tokenRelease(r Resource, l state.Lease) func(context.Context) {
	if r.Release == "" || l.Token == "" || b.ReleaseToken == nil {
		return nil
	}
	return func(ctx context.Context) { _ = b.ReleaseToken(ctx, r, l) }
}

// Stop stops what a displaced lease left running: every group is signalled first, so that nobody
// waits on one holder's grace period before the next is told.
func Stop(ctx context.Context, evicted []Evicted) {
	for _, e := range evicted {
		for _, p := range e.Procs {
			procs.Signal(p)
		}
	}
	for _, e := range evicted {
		for _, p := range e.Procs {
			procs.Finish(p, e.Grace)
		}
		if e.Token != nil {
			e.Token(ctx)
		}
	}
}

// RecordProcs stores a lease's started processes.
func (b *Broker) RecordProcs(ctx context.Context, leaseID int, ps []state.LeaseProc) error {
	return b.Store.Write(ctx, func(tx *sql.Tx) error { return state.SetLeaseProcs(tx, leaseID, ps) })
}

// ErrNotHeld means the caller holds no seat on the resource.
var ErrNotHeld = errors.New("not held")

// Release gives up a lease, or a place in line, and passes the seat on. It returns the processes to
// stop, which the caller does outside the transaction.
func (b *Broker) Release(ctx context.Context, lane int, name string) ([]procs.Proc, time.Duration, error) {
	snap := b.snapshot()
	r := snap.byName[name]
	// A token is given back first: if its script fails, the lease stays, and this can be retried.
	if held, err := state.LeaseOf(b.Store.DB(), lane, name); err == nil && r.Release != "" && held.Token != "" && b.ReleaseToken != nil {
		if err := b.ReleaseToken(ctx, r, held); err != nil {
			return nil, 0, err
		}
	}
	var stop []procs.Proc
	err := b.Store.Write(ctx, func(tx *sql.Tx) error {
		stop = nil
		left, err := state.RemoveFromQueue(tx, lane, name)
		if err != nil {
			return err
		}
		l, err := state.LeaseOf(tx, lane, name)
		if errors.Is(err, state.ErrNotFound) {
			if left {
				return nil
			}
			return ErrNotHeld
		}
		if err != nil {
			return err
		}
		ps, err := state.LeaseProcs(tx, l.ID)
		if err != nil {
			return err
		}
		stop = toProcs(ps)
		if err := state.DeleteLease(tx, l.ID); err != nil {
			return err
		}
		granted, err := b.advance(tx, snap, time.Now())
		if err != nil {
			return err
		}
		return notifyGrants(tx, granted, -1)
	})
	return stop, r.EvictGrace, err
}

// ReleaseLane gives up everything a lane holds or waits for: its own leases, and nobody else's.
func (b *Broker) ReleaseLane(ctx context.Context, lane int) error {
	leases, err := state.Leases(b.Store.DB())
	if err != nil {
		return err
	}
	queue, err := state.QueueEntries(b.Store.DB())
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, l := range leases {
		if l.Lane == lane {
			names[l.Resource] = true
		}
	}
	for _, e := range queue {
		if e.Lane == lane {
			names[e.Resource] = true
		}
	}
	for name := range names {
		stop, grace, err := b.Release(ctx, lane, name)
		if err != nil && !errors.Is(err, ErrNotHeld) {
			return err
		}
		for _, p := range stop {
			procs.Stop(p, grace)
		}
	}
	return nil
}

func dead(l state.Lease, ps []state.LeaseProc) bool {
	if l.State != state.LeaseHeld {
		return false
	}
	if len(ps) > 0 {
		for _, p := range ps {
			if procs.Alive(procs.Proc{PID: p.PID, Start: p.Start}) {
				return false
			}
		}
		return true
	}
	return l.PID > 0 && l.PIDStart != "" && !procs.Alive(procs.Proc{PID: l.PID, Start: l.PIDStart})
}

// Recover frees the seats of holders that have died, stops what they left behind and moves the queue
// on (R42). Every state-changing command calls it. A lease whose process cannot be read is kept.
func (b *Broker) Recover(ctx context.Context) error {
	leases, err := state.Leases(b.Store.DB())
	if err != nil {
		return err
	}
	var gone []state.Lease
	var orphans []procs.Proc
	limits := false
	snap := b.snapshot()
	for _, l := range leases {
		ps, err := state.LeaseProcs(b.Store.DB(), l.ID)
		if err != nil {
			return err
		}
		if dead(l, ps) {
			gone = append(gone, l)
			orphans = append(orphans, toProcs(ps)...)
		}
		r := snap.byName[l.Resource]
		limits = limits || l.State == state.LeaseReserved || r.MaxHold > 0 || r.IdleExpiry > 0
	}
	if len(gone) == 0 && !limits {
		return nil
	}
	now := time.Now()
	var displaced []Evicted
	err = b.Store.Write(ctx, func(tx *sql.Tx) error {
		for _, l := range gone {
			if err := state.DeleteLease(tx, l.ID); err != nil {
				return err
			}
		}
		var err error
		if displaced, err = b.expire(tx, snap, now); err != nil {
			return err
		}
		granted, err := b.advance(tx, snap, now)
		if err != nil {
			return err
		}
		return notifyGrants(tx, granted, -1)
	})
	if err != nil {
		return err
	}
	for _, p := range orphans {
		procs.Stop(p, 2*time.Second)
	}
	for _, l := range gone {
		if release := b.tokenRelease(snap.byName[l.Resource], l); release != nil {
			release(ctx)
		}
	}
	Stop(ctx, displaced)
	return nil
}

// Dead reports whether a lease's holder has gone, for read-only views.
func (b *Broker) Dead(l state.Lease, ps []state.LeaseProc) bool { return dead(l, ps) }
