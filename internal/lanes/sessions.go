package lanes

import (
	"context"
	"database/sql"

	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/state"
)

// Live reports whether a session's process is still running. A session with no recorded process,
// or whose process cannot be checked, counts as live: unknown means protected.
func Live(s state.Session) bool {
	return platform.Alive(s.PID, s.PIDStart)
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
