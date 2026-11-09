package state

import (
	"database/sql"
	"errors"
	"time"
)

// MaxDelegation is the longest a delegation may last. A record that claims longer was not written
// by aw delegate, and counts as none.
const MaxDelegation = 8 * time.Hour

// ScopeMaintenance is the one scope a delegation can have.
const ScopeMaintenance = "maintenance"

// Delegation is the human's grant of routine maintenance to owner-checkout sessions.
type Delegation struct {
	Scope   string    `json:"scope"`
	Granted time.Time `json:"granted"`
	Expires time.Time `json:"expires"`
}

// Grant records a delegation, replacing any before it.
func Grant(tx *sql.Tx, ttl time.Duration) (Delegation, error) {
	now := time.Now().UTC()
	d := Delegation{Scope: ScopeMaintenance, Granted: now, Expires: now.Add(ttl)}
	_, err := tx.Exec(`INSERT INTO delegations (id, scope, granted, expires) VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET scope = excluded.scope, granted = excluded.granted, expires = excluded.expires`,
		d.Scope, d.Granted.Format(time.RFC3339Nano), d.Expires.Format(time.RFC3339Nano))
	return d, err
}

// Revoke removes the delegation.
func Revoke(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM delegations`)
	return err
}

// CurrentDelegation returns the delegation in force. Anything unexpected counts as none: an unknown
// scope, an unreadable time, one that has expired, or one longer than aw delegate can grant.
func CurrentDelegation(q Querier) (Delegation, bool) {
	var d Delegation
	var granted, expires string
	err := q.QueryRow(`SELECT scope, granted, expires FROM delegations WHERE id = 1`).Scan(&d.Scope, &granted, &expires)
	if err != nil || errors.Is(err, sql.ErrNoRows) || d.Scope != ScopeMaintenance {
		return d, false
	}
	var e1, e2 error
	d.Granted, e1 = time.Parse(time.RFC3339Nano, granted)
	d.Expires, e2 = time.Parse(time.RFC3339Nano, expires)
	now := time.Now()
	if e1 != nil || e2 != nil || !d.Expires.After(now) || d.Granted.After(now) || d.Expires.Sub(d.Granted) > MaxDelegation {
		return d, false
	}
	return d, true
}

// DelegationActive reports whether a delegation is in force.
func DelegationActive(q Querier) bool {
	_, ok := CurrentDelegation(q)
	return ok
}

// SetLaneRole records the team role a lane works under. "" is no role.
func SetLaneRole(tx *sql.Tx, lane int, role string) error {
	_, err := tx.Exec(`UPDATE lanes SET role = ? WHERE number = ?`, role, lane)
	return err
}

// MovePairSession turns the live pair session into a session of a lane, when aw move-to-lane moves
// the work it was doing. It returns how many sessions moved.
func MovePairSession(tx *sql.Tx, lane, generation int) (int64, error) {
	res, err := tx.Exec(`UPDATE sessions SET pair = 0, lane = ?, lane_generation = ? WHERE pair = 1 AND state = ?`, lane, generation, SessionActive)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
