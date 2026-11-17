package lanes

import (
	"context"
	"database/sql"
	"errors"

	"github.com/connor981332/agent-workspace/internal/state"
)

// Live reports whether a session's process is still running. A session with no recorded process,
// or whose process cannot be checked, counts as live: unknown means protected.
func Live(s state.Session) bool {
	return s.Alive()
}

// Recover marks active sessions whose process has gone (a crash, a closed terminal, a reboot) as
// interrupted, so they no longer hold their lanes. The lanes stay (R23, R80). State-changing
// commands call it; read-only views only compute it.
func (m *Manager) Recover(ctx context.Context) error {
	sessions, err := state.Sessions(m.Store.DB())
	if err != nil {
		return err
	}
	var dead []int
	for _, s := range sessions {
		if s.State == state.SessionActive && !Live(s) {
			dead = append(dead, s.ID)
		}
	}
	if len(dead) == 0 {
		return nil
	}
	return m.Store.Write(ctx, func(tx *sql.Tx) error {
		for _, id := range dead {
			if _, err := tx.Exec(`UPDATE sessions SET state = ? WHERE id = ? AND state = ?`, state.SessionInterrupted, id, state.SessionActive); err != nil {
				return err
			}
		}
		return nil
	})
}

// Detached is what aw detach did.
type Detached struct {
	Lane     *state.Lane `json:"lane,omitempty"` // the lane released, or nil when the session held none
	Home     string      `json:"home"`           // the scope the session is in now
	Released bool        `json:"released"`
}

// Detach ends a session's hold on its lane and returns it to its home (docs/spec/sessions.md): the
// owner checkout, or the lane itself when it started there. It cannot widen anything. Detaching a
// session that holds nothing changes nothing.
func (m *Manager) Detach(ctx context.Context, who Identity) (*Detached, error) {
	if !who.known() {
		return nil, errNoSession
	}
	d := &Detached{Home: state.HomeUnattached}
	err := m.Store.Write(ctx, func(tx *sql.Tx) error {
		s, err := state.SessionByHarnessID(tx, who.Harness, who.Session)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		d.Home = s.Home
		if s.Pair {
			d.Home = state.HomePair
			return nil
		}
		if s.Lane == 0 || s.Released != "" {
			return nil
		}
		l, err := state.LaneByNumber(tx, s.Lane)
		if err != nil || l.Generation != s.LaneGeneration {
			return nil // its lane has moved on: it held nothing
		}
		d.Lane, d.Released = &l, true
		return state.ReleaseSession(tx, s.ID)
	})
	return d, err
}

var errNoSession = errors.New("this is not a harness session that aw can identify, so it holds no lane → run it from the session, or end the session itself")
