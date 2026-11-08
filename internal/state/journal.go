package state

import (
	"database/sql"
	"errors"
)

// Journal statuses.
const (
	JournalRunning = "running"
	JournalDone    = "done"
	JournalFailed  = "failed"
)

// JournalEntry is one step of a journaled operation, such as a lane's recycling.
type JournalEntry struct {
	ID      int    `json:"id"`
	Kind    string `json:"kind"`
	Lane    int    `json:"lane"`
	Step    string `json:"step"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Started string `json:"started"`
	Ended   string `json:"ended,omitempty"`
}

// StartJournal records an operation's start and returns its id.
func StartJournal(tx *sql.Tx, kind string, lane int, step string) (int, error) {
	var id int
	err := tx.QueryRow(`INSERT INTO journal (kind, lane, step, status, started) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		kind, lane, step, JournalRunning, now()).Scan(&id)
	return id, err
}

// EndJournal records how an operation ended.
func EndJournal(tx *sql.Tx, id int, status, detail string) error {
	_, err := tx.Exec(`UPDATE journal SET status = ?, detail = ?, ended = ? WHERE id = ?`, status, detail, now(), id)
	return err
}

// OpenJournal returns the operations that did not finish: failed, or still running (a crash).
func OpenJournal(q Querier) ([]JournalEntry, error) {
	rows, err := q.Query(`SELECT id, kind, coalesce(lane, 0), step, status, detail, started, coalesce(ended, '') FROM journal
		WHERE status != ? ORDER BY id`, JournalDone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JournalEntry
	for rows.Next() {
		var e JournalEntry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Lane, &e.Step, &e.Status, &e.Detail, &e.Started, &e.Ended); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// JournalByID returns one entry.
func JournalByID(q Querier, id int) (JournalEntry, error) {
	var e JournalEntry
	err := q.QueryRow(`SELECT id, kind, coalesce(lane, 0), step, status, detail, started, coalesce(ended, '') FROM journal WHERE id = ?`, id).
		Scan(&e.ID, &e.Kind, &e.Lane, &e.Step, &e.Status, &e.Detail, &e.Started, &e.Ended)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// Handoff is what aw finish recorded.
type Handoff struct {
	ID         int    `json:"id"`
	Lane       int    `json:"lane"`
	Generation int    `json:"generation"`
	Created    string `json:"created"`
	Summary    string `json:"summary"`
	Plan       string `json:"plan,omitempty"`
	Heads      string `json:"heads"` // JSON: project → head
	// Archived is when aw open picked it up, or "".
	Archived string `json:"archived,omitempty"`
}

// AddHandoff records a handoff.
func AddHandoff(tx *sql.Tx, h Handoff) error {
	_, err := tx.Exec(`INSERT INTO handoffs (lane, generation, created, summary, plan, heads) VALUES (?, ?, ?, ?, ?, ?)`,
		h.Lane, h.Generation, now(), h.Summary, h.Plan, h.Heads)
	return err
}

// ArchiveHandoff records that aw open has picked a handoff up.
func ArchiveHandoff(tx *sql.Tx, id int) error {
	_, err := tx.Exec(`UPDATE handoffs SET archived = ? WHERE id = ? AND archived = ''`, now(), id)
	return err
}

// LatestHandoff returns the newest handoff of a lane's current generation.
func LatestHandoff(q Querier, lane, generation int) (Handoff, error) {
	var h Handoff
	err := q.QueryRow(`SELECT id, lane, generation, created, summary, plan, heads, archived FROM handoffs
		WHERE lane = ? AND generation = ? ORDER BY id DESC LIMIT 1`, lane, generation).
		Scan(&h.ID, &h.Lane, &h.Generation, &h.Created, &h.Summary, &h.Plan, &h.Heads, &h.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return h, ErrNotFound
	}
	return h, err
}
