package state

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// Lane states (lifecycle.md, "States").
const (
	LaneClaiming   = "claiming" // reserved; its worktrees are being created
	LanePool       = "pool"
	LaneSettingUp  = "setting-up"
	LaneWorking    = "working"
	LaneIncomplete = "incomplete"
	LaneReady      = "ready"
	LaneParked     = "parked"
)

// Lane is one row of lanes.
type Lane struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Purpose    string `json:"purpose"`
	State      string `json:"state"`
	Generation int    `json:"generation"`
	Created    string `json:"created"`
	Claimed    string `json:"claimed,omitempty"`
	Updated    string `json:"updated"`
	Pooled     string `json:"pooled,omitempty"`
}

// LaneRepo is a lane's worktree of one repository. Project is "" for the workspace repository in
// a multi-repository workspace.
type LaneRepo struct {
	Lane    int    `json:"lane"`
	Project string `json:"project"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	BaseRef string `json:"base_ref"`
	BaseSHA string `json:"base_sha"`
}

// Session is one harness session.
type Session struct {
	ID             int    `json:"id"`
	Harness        string `json:"harness"`
	HarnessSession string `json:"harness_session"`
	Lane           int    `json:"lane,omitempty"`
	LaneGeneration int    `json:"lane_generation,omitempty"`
	Purpose        string `json:"purpose"`
	State          string `json:"state"`
	Started        string `json:"started"`
	LastActivity   string `json:"last_activity"`
	PID            int    `json:"pid,omitempty"`
	PIDStart       string `json:"pid_start,omitempty"`
}

// Session states.
const (
	SessionActive      = "active"
	SessionInterrupted = "interrupted"
	SessionEnded       = "ended"
)

// Querier is *sql.DB or *sql.Tx.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

const laneColumns = `number, coalesce(name, ''), purpose, state, generation, created, coalesce(claimed, ''), updated, coalesce(pooled, '')`

func scanLane(row interface{ Scan(...any) error }) (Lane, error) {
	var l Lane
	err := row.Scan(&l.Number, &l.Name, &l.Purpose, &l.State, &l.Generation, &l.Created, &l.Claimed, &l.Updated, &l.Pooled)
	return l, err
}

// Lanes returns every lane, by number.
func Lanes(q Querier) ([]Lane, error) {
	rows, err := q.Query(`SELECT ` + laneColumns + ` FROM lanes ORDER BY number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lane
	for rows.Next() {
		l, err := scanLane(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ErrNotFound means no such record.
var ErrNotFound = errors.New("not found")

// LaneByNumber returns one lane.
func LaneByNumber(q Querier, n int) (Lane, error) {
	l, err := scanLane(q.QueryRow(`SELECT `+laneColumns+` FROM lanes WHERE number = ?`, n))
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

// LaneByName returns the lane currently called name.
func LaneByName(q Querier, name string) (Lane, error) {
	l, err := scanLane(q.QueryRow(`SELECT `+laneColumns+` FROM lanes WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

// InsertLane adds a lane.
func InsertLane(tx *sql.Tx, l Lane) error {
	_, err := tx.Exec(`INSERT INTO lanes (number, name, purpose, state, generation, created, claimed, updated, pooled)
		VALUES (?, nullif(?, ''), ?, ?, ?, ?, nullif(?, ''), ?, nullif(?, ''))`,
		l.Number, l.Name, l.Purpose, l.State, l.Generation, l.Created, l.Claimed, l.Updated, l.Pooled)
	return err
}

// UpdateLane writes every column of a lane.
func UpdateLane(tx *sql.Tx, l Lane) error {
	res, err := tx.Exec(`UPDATE lanes SET name = nullif(?, ''), purpose = ?, state = ?, generation = ?, claimed = nullif(?, ''),
		updated = ?, pooled = nullif(?, '') WHERE number = ?`,
		l.Name, l.Purpose, l.State, l.Generation, l.Claimed, l.Updated, l.Pooled, l.Number)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("lane %02d: %w", l.Number, ErrNotFound)
	}
	return nil
}

// DeleteLane removes a lane and its repositories.
func DeleteLane(tx *sql.Tx, n int) error {
	_, err := tx.Exec(`DELETE FROM lanes WHERE number = ?`, n)
	return err
}

// LaneRepos returns a lane's repositories, the workspace repository first.
func LaneRepos(q Querier, lane int) ([]LaneRepo, error) {
	rows, err := q.Query(`SELECT lane, project, path, branch, base_ref, base_sha FROM lane_repos WHERE lane = ? ORDER BY project`, lane)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LaneRepo
	for rows.Next() {
		var r LaneRepo
		if err := rows.Scan(&r.Lane, &r.Project, &r.Path, &r.Branch, &r.BaseRef, &r.BaseSHA); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetLaneRepos replaces a lane's repositories.
func SetLaneRepos(tx *sql.Tx, lane int, repos []LaneRepo) error {
	if _, err := tx.Exec(`DELETE FROM lane_repos WHERE lane = ?`, lane); err != nil {
		return err
	}
	for _, r := range repos {
		if _, err := tx.Exec(`INSERT INTO lane_repos (lane, project, path, branch, base_ref, base_sha) VALUES (?, ?, ?, ?, ?, ?)`,
			lane, r.Project, r.Path, r.Branch, r.BaseRef, r.BaseSHA); err != nil {
			return err
		}
	}
	return nil
}

const sessionColumns = `id, harness, harness_session, coalesce(lane, 0), coalesce(lane_generation, 0), purpose, state, started, last_activity, coalesce(pid, 0), coalesce(pid_start, '')`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.Harness, &s.HarnessSession, &s.Lane, &s.LaneGeneration, &s.Purpose, &s.State, &s.Started, &s.LastActivity, &s.PID, &s.PIDStart)
	return s, err
}

// SessionByHarnessID returns the session a harness knows by id.
func SessionByHarnessID(q Querier, harness, id string) (Session, error) {
	s, err := scanSession(q.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE harness = ? AND harness_session = ?`, harness, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// RecordSessionIdentity is the hook's write (speed.md, "The hot path"): a session's own ID on its
// first hook call, and its process when that was not known. It never waits for a lock: if the
// database is busy it returns an error, and the next call tries again.
func RecordSessionIdentity(dir string, id int, harnessSession string, pid int, pidStart string) error {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, File)+"?_pragma=busy_timeout(0)")
	if err != nil {
		return err
	}
	defer db.Close()
	var p any
	if pid > 0 {
		p = pid
	}
	_, err = db.Exec(`UPDATE sessions SET harness_session = ?, pid = coalesce(?, pid), pid_start = coalesce(nullif(?, ''), pid_start)
		WHERE id = ? AND state = ?`, harnessSession, p, pidStart, id, SessionActive)
	return err
}

// Sessions returns every session, newest first.
func Sessions(q Querier) ([]Session, error) {
	rows, err := q.Query(`SELECT ` + sessionColumns + ` FROM sessions ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SaveSession inserts or updates a session by (harness, harness_session), and returns its id.
func SaveSession(tx *sql.Tx, s Session) (int, error) {
	var lane, gen, pid any
	if s.Lane > 0 {
		lane, gen = s.Lane, s.LaneGeneration
	}
	if s.PID > 0 {
		pid = s.PID
	}
	var id int
	err := tx.QueryRow(`INSERT INTO sessions (harness, harness_session, lane, lane_generation, purpose, state, started, last_activity, pid, pid_start)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, nullif(?, ''))
		ON CONFLICT (harness, harness_session) DO UPDATE SET lane = excluded.lane, lane_generation = excluded.lane_generation,
			purpose = excluded.purpose, state = excluded.state, last_activity = excluded.last_activity,
			pid = coalesce(excluded.pid, sessions.pid), pid_start = coalesce(excluded.pid_start, sessions.pid_start)
		RETURNING id`,
		s.Harness, s.HarnessSession, lane, gen, s.Purpose, s.State, s.Started, s.LastActivity, pid, s.PIDStart).Scan(&id)
	return id, err
}

// Now is the time format stored in state: UTC, RFC 3339 with nanoseconds.
func Now() string { return now() }
