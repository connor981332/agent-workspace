package state

import (
	"database/sql"
	"errors"
	"time"
)

// Statuses of a setup step.
const (
	StepPending = "pending"
	StepRunning = "running"
	StepDone    = "done"
	StepSkipped = "skipped" // an install whose stamp did not change
	StepFailed  = "failed"
)

// Step is one setup step of a lane, as the lifecycle journal has it.
type Step struct {
	Lane       int    `json:"lane"`
	Generation int    `json:"generation"`
	Name       string `json:"step"`
	Seq        int    `json:"seq"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	Log        string `json:"log,omitempty"`
	Exit       *int   `json:"exit_status,omitempty"`
	Started    string `json:"started,omitempty"`
	Ended      string `json:"ended,omitempty"`
}

// Finished reports whether a step needs no more work.
func (s Step) Finished() bool { return s.Status == StepDone || s.Status == StepSkipped }

// Setup is a lane's setup: what it set up, and the process running it, when one is.
type Setup struct {
	Lane       int
	Generation int
	FromPool   bool
	PID        int
	PIDStart   string
	Created    string
}

// PlanSetup records the steps a lane's setup runs, in order. A step that finished in this generation
// stays finished, so a retry reruns from the first incomplete step; anything else is pending again.
// Steps of earlier generations, and steps the configuration no longer has, are forgotten.
func PlanSetup(tx *sql.Tx, lane, generation int, fromPool bool, names []string) error {
	if _, err := tx.Exec(`DELETE FROM lane_steps WHERE lane = ? AND generation != ?`, lane, generation); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, n := range names {
		keep[n] = true
	}
	existing, err := Steps(tx, lane, generation)
	if err != nil {
		return err
	}
	for _, s := range existing {
		if !keep[s.Name] {
			if _, err := tx.Exec(`DELETE FROM lane_steps WHERE lane = ? AND generation = ? AND step = ?`, lane, generation, s.Name); err != nil {
				return err
			}
		}
	}
	for i, n := range names {
		if _, err := tx.Exec(`INSERT INTO lane_steps (lane, generation, step, seq, status) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (lane, generation, step) DO UPDATE SET seq = excluded.seq,
				status = CASE WHEN status IN ('done', 'skipped') THEN status ELSE 'pending' END`,
			lane, generation, n, i, StepPending); err != nil {
			return err
		}
	}
	pooled := 0
	if fromPool {
		pooled = 1
	}
	_, err = tx.Exec(`INSERT INTO lane_setups (lane, generation, from_pool, created) VALUES (?, ?, ?, ?)
		ON CONFLICT (lane) DO UPDATE SET generation = excluded.generation, from_pool = excluded.from_pool,
			created = CASE WHEN generation = excluded.generation THEN created ELSE excluded.created END`,
		lane, generation, pooled, now())
	return err
}

// Steps returns a lane's steps for one generation, in order.
func Steps(q Querier, lane, generation int) ([]Step, error) {
	rows, err := q.Query(`SELECT lane, generation, step, seq, status, detail, log, exit_status, started, ended
		FROM lane_steps WHERE lane = ? AND generation = ? ORDER BY seq`, lane, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var s Step
		var exit sql.NullInt64
		if err := rows.Scan(&s.Lane, &s.Generation, &s.Name, &s.Seq, &s.Status, &s.Detail, &s.Log, &exit, &s.Started, &s.Ended); err != nil {
			return nil, err
		}
		if exit.Valid {
			v := int(exit.Int64)
			s.Exit = &v
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// StartStep records that a step is running, and where its log is.
func StartStep(tx *sql.Tx, lane, generation int, step, log string) error {
	_, err := tx.Exec(`UPDATE lane_steps SET status = ?, log = ?, detail = '', exit_status = NULL, started = ?, ended = ''
		WHERE lane = ? AND generation = ? AND step = ?`, StepRunning, log, now(), lane, generation, step)
	return err
}

// EndStep records how a step ended. exit is its exit status, or nil when it never ran to one.
func EndStep(tx *sql.Tx, lane, generation int, step, status, detail string, exit *int) error {
	_, err := tx.Exec(`UPDATE lane_steps SET status = ?, detail = ?, exit_status = ?, ended = ?
		WHERE lane = ? AND generation = ? AND step = ?`, status, detail, exit, now(), lane, generation, step)
	return err
}

// FailRunningSteps marks a lane's running steps failed, for a setup whose process has gone.
func FailRunningSteps(tx *sql.Tx, lane, generation int, detail string) error {
	_, err := tx.Exec(`UPDATE lane_steps SET status = ?, detail = ?, ended = ? WHERE lane = ? AND generation = ? AND status = ?`,
		StepFailed, detail, now(), lane, generation, StepRunning)
	return err
}

// LaneSetup returns a lane's setup, or ErrNotFound.
func LaneSetup(q Querier, lane int) (Setup, error) {
	var s Setup
	var pooled int
	err := q.QueryRow(`SELECT lane, generation, from_pool, pid, pid_start, created FROM lane_setups WHERE lane = ?`, lane).
		Scan(&s.Lane, &s.Generation, &pooled, &s.PID, &s.PIDStart, &s.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	s.FromPool = pooled == 1
	return s, err
}

// SetSetupRunner records the process running a lane's setup. PID 0 records that none is.
func SetSetupRunner(tx *sql.Tx, lane, pid int, start string) error {
	_, err := tx.Exec(`UPDATE lane_setups SET pid = ?, pid_start = ? WHERE lane = ?`, pid, start, lane)
	return err
}

// ForgetSetup removes a lane's setup and its steps, when the lane is destroyed.
func ForgetSetup(tx *sql.Tx, lane int) error {
	if _, err := tx.Exec(`DELETE FROM lane_steps WHERE lane = ?`, lane); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM lane_setups WHERE lane = ?`, lane)
	return err
}

// ActiveSetups returns the setups that have a process, or whose lane is still setting up.
func ActiveSetups(q Querier) ([]Setup, error) {
	rows, err := q.Query(`SELECT s.lane, s.generation, s.from_pool, s.pid, s.pid_start, s.created
		FROM lane_setups s JOIN lanes l ON l.number = s.lane WHERE s.pid > 0 OR l.state = ?`, LaneSettingUp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Setup
	for rows.Next() {
		var s Setup
		var pooled int
		if err := rows.Scan(&s.Lane, &s.Generation, &pooled, &s.PID, &s.PIDStart, &s.Created); err != nil {
			return nil, err
		}
		s.FromPool = pooled == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

// Age is how long ago the setup was recorded, for a setup whose process has not started.
func (s Setup) Age() time.Duration {
	t, err := time.Parse(time.RFC3339Nano, s.Created)
	if err != nil {
		return 0
	}
	return time.Since(t)
}
