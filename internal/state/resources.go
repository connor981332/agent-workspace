package state

import (
	"database/sql"
	"errors"
	"path/filepath"
)

// Lease states.
const (
	LeaseReserved = "reserved" // granted to a lane that was not waiting: it has a claim window (S2)
	LeaseHeld     = "held"
)

// Queue priorities, highest first.
const (
	QueueNormal  = 0
	QueueEvicted = 1 // an evicted lane goes back to the head (R40)
	QueueOwner   = 2
)

// Lease is one seat on a resource.
type Lease struct {
	ID           int    `json:"id"`
	Resource     string `json:"resource"`
	Lane         int    `json:"lane"` // 0 is the owner
	Generation   int    `json:"generation,omitempty"`
	LaneName     string `json:"lane_name,omitempty"`
	Seat         int    `json:"seat"`
	State        string `json:"state"`
	Token        string `json:"token,omitempty"`
	Granted      string `json:"granted"`
	ClaimBy      string `json:"claim_by,omitempty"`
	LastActivity string `json:"last_activity"`
	PID          int    `json:"pid,omitempty"`
	PIDStart     string `json:"pid_start,omitempty"`
}

// LeaseProc is one process a lease started.
type LeaseProc struct {
	Server string `json:"server"`
	PID    int    `json:"pid"`
	Start  string `json:"start"`
	Log    string `json:"log"`
}

// QueueEntry is one lane in line for one resource. Entries of one request are granted together.
type QueueEntry struct {
	ID         int    `json:"id"`
	Request    string `json:"-"`
	Resource   string `json:"resource"`
	Lane       int    `json:"lane"`
	Generation int    `json:"-"`
	LaneName   string `json:"lane_name,omitempty"`
	Priority   int    `json:"priority"`
	Enqueued   string `json:"enqueued"`
}

// Notification waits for an agent's next tool call.
type Notification struct {
	ID      int
	Lane    int
	Kind    string
	Message string
}

const leaseColumns = `id, resource, lane, generation, lane_name, seat, state, token, granted, claim_by, last_activity, pid, pid_start`

func scanLease(row interface{ Scan(...any) error }) (Lease, error) {
	var l Lease
	err := row.Scan(&l.ID, &l.Resource, &l.Lane, &l.Generation, &l.LaneName, &l.Seat, &l.State, &l.Token, &l.Granted, &l.ClaimBy, &l.LastActivity, &l.PID, &l.PIDStart)
	return l, err
}

// Leases returns every lease, in a stable order.
func Leases(q Querier) ([]Lease, error) {
	rows, err := q.Query(`SELECT ` + leaseColumns + ` FROM leases ORDER BY resource, seat`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LeaseOf returns a lane's lease on a resource.
func LeaseOf(q Querier, lane int, resource string) (Lease, error) {
	l, err := scanLease(q.QueryRow(`SELECT `+leaseColumns+` FROM leases WHERE lane = ? AND resource = ?`, lane, resource))
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

// InsertLease records a lease and returns its id.
func InsertLease(tx *sql.Tx, l Lease) (int, error) {
	var id int
	err := tx.QueryRow(`INSERT INTO leases (resource, lane, generation, lane_name, seat, state, token, granted, claim_by, last_activity, pid, pid_start)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		l.Resource, l.Lane, l.Generation, l.LaneName, l.Seat, l.State, l.Token, l.Granted, l.ClaimBy, l.LastActivity, l.PID, l.PIDStart).Scan(&id)
	return id, err
}

// UpdateLease saves a lease's state.
func UpdateLease(tx *sql.Tx, l Lease) error {
	_, err := tx.Exec(`UPDATE leases SET state = ?, pid = ?, pid_start = ?, last_activity = ? WHERE id = ?`, l.State, l.PID, l.PIDStart, l.LastActivity, l.ID)
	return err
}

// DeleteLease removes a lease and its processes' records.
func DeleteLease(tx *sql.Tx, id int) error {
	if _, err := tx.Exec(`DELETE FROM lease_procs WHERE lease = ?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM leases WHERE id = ?`, id)
	return err
}

// LeaseProcs returns the processes a lease started.
func LeaseProcs(q Querier, id int) ([]LeaseProc, error) {
	rows, err := q.Query(`SELECT server, pid, pid_start, log FROM lease_procs WHERE lease = ? ORDER BY server`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeaseProc
	for rows.Next() {
		var p LeaseProc
		if err := rows.Scan(&p.Server, &p.PID, &p.Start, &p.Log); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetLeaseProcs records a lease's processes.
func SetLeaseProcs(tx *sql.Tx, id int, ps []LeaseProc) error {
	for _, p := range ps {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO lease_procs (lease, server, pid, pid_start, log) VALUES (?, ?, ?, ?, ?)`, id, p.Server, p.PID, p.Start, p.Log); err != nil {
			return err
		}
	}
	return nil
}

// QueueEntries returns the queue in the order it is served.
func QueueEntries(q Querier) ([]QueueEntry, error) {
	rows, err := q.Query(`SELECT id, request, resource, lane, generation, lane_name, priority, enqueued FROM queue ORDER BY priority DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueueEntry
	for rows.Next() {
		var e QueueEntry
		if err := rows.Scan(&e.ID, &e.Request, &e.Resource, &e.Lane, &e.Generation, &e.LaneName, &e.Priority, &e.Enqueued); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Enqueue adds a lane to a resource's queue. A lane has at most one entry per resource.
func Enqueue(tx *sql.Tx, e QueueEntry) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO queue (request, resource, lane, generation, lane_name, priority, enqueued) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Request, e.Resource, e.Lane, e.Generation, e.LaneName, e.Priority, now())
	return err
}

// RemoveRequest removes every entry of a granted request.
func RemoveRequest(tx *sql.Tx, request string) error {
	_, err := tx.Exec(`DELETE FROM queue WHERE request = ?`, request)
	return err
}

// RemoveFromQueue takes a lane out of a resource's queue, and reports whether it was in it. Other
// resources of the same request go too, since a request is granted whole.
func RemoveFromQueue(tx *sql.Tx, lane int, resource string) (bool, error) {
	var request string
	err := tx.QueryRow(`SELECT request FROM queue WHERE lane = ? AND resource = ?`, lane, resource).Scan(&request)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, RemoveRequest(tx, request)
}

// AddNotification queues a message for a lane's next tool call. The owner has none to receive it.
func AddNotification(tx *sql.Tx, lane int, kind, message string) error {
	if lane == 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO notifications (lane, kind, message, created) VALUES (?, ?, ?, ?)`, lane, kind, message, now())
	return err
}

// PendingNotifications are a lane's undelivered messages, oldest first.
func PendingNotifications(q Querier, lane int) ([]Notification, error) {
	rows, err := q.Query(`SELECT id, lane, kind, message FROM notifications WHERE lane = ? AND delivered = '' ORDER BY id`, lane)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Lane, &n.Kind, &n.Message); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkDelivered is the hook's write for notifications (S6). It never waits for a lock: an undelivered
// message is sent again on the next call, and its ID makes a repeat recognizable.
func MarkDelivered(dir string, ids []int) error {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, File)+"?_pragma=busy_timeout(0)")
	if err != nil {
		return err
	}
	defer db.Close()
	for _, id := range ids {
		if _, err := db.Exec(`UPDATE notifications SET delivered = ? WHERE id = ?`, now(), id); err != nil {
			return err
		}
	}
	return nil
}
