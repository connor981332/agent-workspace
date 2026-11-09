package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/extproto"
)

// externalTimeout is how long an external provider's verb may take: creating data can be slow.
const externalTimeout = 10 * time.Minute

// external is a provider executable that speaks the extension protocol.
type external struct {
	name  string
	data  *config.Data
	owner string
}

func (e *external) call(ctx context.Context, verb string, l Lane, extra map[string]any) (map[string]any, error) {
	req := map[string]any{"lane": l, "data": e.name, "config": e.data}
	for k, v := range extra {
		req[k] = v
	}
	return extproto.CallWithin(ctx, externalTimeout, e.owner, e.data.Command, verb, req)
}

func strings_(reply map[string]any, key string) ([]string, error) {
	raw, ok := reply[key]
	if !ok {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("its %q must be an array of strings", key)
	}
	out := make([]string, len(list))
	for i, v := range list {
		if out[i], ok = v.(string); !ok {
			return nil, fmt.Errorf("its %q must be an array of strings", key)
		}
	}
	return out, nil
}

func (e *external) records(reply map[string]any, owned []string, record func(string) error) error {
	names, err := strings_(reply, "names")
	if err != nil {
		return fmt.Errorf("%s: %w", e.data.Command, err)
	}
	have := map[string]bool{}
	for _, n := range owned {
		have[n] = true
	}
	for _, n := range names {
		if !have[n] {
			if err := record(n); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *external) Create(ctx context.Context, l Lane, owned []string, record func(string) error) error {
	reply, err := e.call(ctx, "create", l, map[string]any{"names": owned})
	if err != nil {
		return err
	}
	return e.records(reply, owned, record)
}

func (e *external) Reset(ctx context.Context, l Lane, owned []string, record func(string) error) error {
	reply, err := e.call(ctx, "reset", l, map[string]any{"names": owned})
	if err != nil {
		return err
	}
	return e.records(reply, owned, record)
}

func (e *external) Env(ctx context.Context, l Lane, owned []string) (map[string]string, error) {
	reply, err := e.call(ctx, "env", l, map[string]any{"names": owned})
	if err != nil {
		return nil, err
	}
	raw, ok := reply["env"].(map[string]any)
	if !ok {
		if _, present := reply["env"]; present {
			return nil, fmt.Errorf("%s env: its \"env\" must be an object of strings", e.data.Command)
		}
		return map[string]string{}, nil
	}
	out := map[string]string{}
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s env: the value of %s must be a string", e.data.Command, k)
		}
		out[k] = s
	}
	return out, nil
}

func (e *external) Discover(context.Context, Lane, []string, []string) ([]string, error) {
	return nil, nil
}

func (e *external) Drop(ctx context.Context, l Lane, name string) error {
	_, err := e.call(ctx, "drop", l, map[string]any{"name": name})
	return err
}

func (e *external) Snapshot(ctx context.Context, l Lane, name string, owned []string) ([]string, error) {
	reply, err := e.call(ctx, "snapshot", l, map[string]any{"snapshot": name, "names": owned})
	if err != nil {
		return nil, err
	}
	return strings_(reply, "objects")
}

func (e *external) Restore(ctx context.Context, l Lane, name string, objects, owned []string) error {
	_, err := e.call(ctx, "restore", l, map[string]any{"snapshot": name, "objects": objects, "names": owned})
	return err
}

func (e *external) Describe(ctx context.Context, l Lane, owned []string) (string, error) {
	reply, err := e.call(ctx, "describe", l, map[string]any{"names": owned})
	if err != nil {
		return "", err
	}
	s, _ := reply["description"].(string)
	return s, nil
}
