package state

import (
	"database/sql"
	"errors"
)

// Note is something a lane's agent wrote down to do before the lane is done (aw note).
type Note struct {
	ID      int    `json:"id"`
	Text    string `json:"text"`
	Created string `json:"created"`
	Done    string `json:"done,omitempty"`
}

// AddNote records a note on a lane's current generation, and returns its number.
func AddNote(tx *sql.Tx, lane, generation int, text string) (int, error) {
	res, err := tx.Exec(`INSERT INTO lane_notes (lane, generation, text, created) VALUES (?, ?, ?, ?)`, lane, generation, text, Now())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// ErrNoNote is returned for a note the lane does not have open.
var ErrNoNote = errors.New("no such open note")

// CloseNote marks one of a lane's open notes done.
func CloseNote(tx *sql.Tx, lane, generation, id int) error {
	res, err := tx.Exec(`UPDATE lane_notes SET done = ? WHERE id = ? AND lane = ? AND generation = ? AND done = ''`, Now(), id, lane, generation)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoNote
	}
	return nil
}

// OpenNotes are a lane generation's notes not yet done, oldest first.
func OpenNotes(q Querier, lane, generation int) ([]Note, error) {
	rows, err := q.Query(`SELECT id, text, created FROM lane_notes WHERE lane = ? AND generation = ? AND done = '' ORDER BY id`, lane, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Text, &n.Created); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
