package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lifecycle"
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
	if err := checkSandbox(dir); err != nil {
		return nil, err
	}
	store, err := state.Open(dir, state.ReadWrite)
	if err != nil {
		return nil, err
	}
	if ro := store.ReadOnly(); ro != nil {
		store.Close()
		return nil, ro
	}
	w := &writeContext{ws: ws, cfg: cfg, store: store}
	// Every state-changing command first recovers sessions whose process has gone (R80).
	if err := manager(w).Recover(context.Background()); err != nil {
		store.Close()
		return nil, err
	}
	if err := newBroker(w).Recover(context.Background()); err != nil {
		store.Close()
		return nil, err
	}
	if err := lifecycle.Recover(context.Background(), store); err != nil {
		store.Close()
		return nil, err
	}
	return w, nil
}

// checkSandbox names the setting to change when Codex's sandbox keeps the state directory read-only
// (plan risk 7): SQLite's own error would say only that it cannot write.
func checkSandbox(dir string) error {
	if os.Getenv("CODEX_THREAD_ID") == "" {
		return nil
	}
	if _, err := os.Stat(dir); err != nil {
		return nil // nothing there yet: state.Open reports what is wrong
	}
	f, err := os.CreateTemp(dir, ".sandbox-probe-*")
	if err != nil {
		root := filepath.Dir(dir)
		return fmt.Errorf("aw cannot write its state in %s, because Codex's sandbox keeps it read-only → start Codex with -c 'sandbox_workspace_write.writable_roots=[\"%s\"]', or add that to its configuration", dir, root)
	}
	f.Close()
	return os.Remove(f.Name())
}
