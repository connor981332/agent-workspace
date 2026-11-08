package cli

import (
	"os"
	"path/filepath"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// writeContext is what a state-changing command works with.
type writeContext struct {
	ws    *workspace.Workspace
	cfg   *config.Config
	store *state.Store
}

func (w *writeContext) Close() { w.store.Close() }

// openForWrite finds the workspace, loads the configuration (and refreshes the compiled cache if it
// is stale, which only state-changing commands do), and opens the state for writing.
func openForWrite() (*writeContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	ws, err := workspace.Find(cwd)
	if err != nil {
		return nil, err
	}
	dir, err := ws.StateDir()
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(dir, "config.cache")
	cfg, err := config.ReadCache(cache, frameworkVersion())
	if err != nil {
		if cfg, err = config.Load(ws.Owner); err != nil {
			return nil, err
		}
		_ = config.WriteCache(cache, frameworkVersion(), cfg) // a cache is an optimization; never fail on it
	}
	store, err := state.Open(dir, state.ReadWrite)
	if err != nil {
		return nil, err
	}
	if ro := store.ReadOnly(); ro != nil {
		store.Close()
		return nil, ro
	}
	return &writeContext{ws: ws, cfg: cfg, store: store}, nil
}
