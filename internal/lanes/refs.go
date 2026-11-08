package lanes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
)

const refTokenLife = 15 * time.Minute

// authorizeRefs lets this command's own Git operations change refs from inside a lane, past the
// reference-transaction hook: recycling a lane creates its next branch from within it. The token is
// short-lived and recorded in state (S3), and only aw's Git commands carry it, not the commands an
// agent runs.
func (m *Manager) authorizeRefs(ctx context.Context) error {
	if len(run.ExtraEnv) > 0 {
		return nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := hex.EncodeToString(b)
	if err := m.Store.Write(ctx, func(tx *sql.Tx) error {
		return state.IssueRefToken(tx, token, time.Now().Add(refTokenLife))
	}); err != nil {
		return err
	}
	run.ExtraEnv = []string{"AW_REF_TOKEN=" + token}
	return nil
}
