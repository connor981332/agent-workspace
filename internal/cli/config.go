package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// loadConfig finds the workspace from the working directory and returns its configuration. It
// uses the compiled cache when it is fresh and otherwise reads the layers, but never writes: the
// commands that use it are read-only.
func loadConfig() (*workspace.Workspace, *config.Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	ws, err := workspace.Find(cwd)
	if err != nil {
		return nil, nil, err
	}
	if dir, err := ws.StateDir(); err == nil {
		if c, err := config.ReadCache(filepath.Join(dir, "config.cache"), frameworkVersion()); err == nil {
			return ws, c, nil
		}
	}
	c, err := config.Load(ws.Owner)
	return ws, c, err
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and check the workspace configuration",
		Long: `Read and check the workspace configuration.

The configuration has three layers: framework defaults, the team layer (workspace.toml) and the
individual layer (~/.config/agent-workspace/<namespace>.toml). Keys are dotted paths, such as
lanes.max or projects.web.runner. The reference is docs/reference/config.md.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error { return cmd.Help() }),
	}
	cmd.AddCommand(newConfigGetCmd(), newConfigExplainCmd(), newConfigValidateCmd())
	return cmd
}

func newConfigGetCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Print the effective value of a key",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			path, err := config.ParsePath(args[0])
			if err != nil {
				return fmt.Errorf("%q is not a key: %v → write a dotted key, such as lanes.max", args[0], err)
			}
			_, c, err := loadConfig()
			if err != nil {
				return err
			}
			if err := unknownName(c, path); err != nil {
				return err
			}
			v, ok := c.Get(path)
			if !ok {
				if _, known := c.Explain(path); known {
					return fmt.Errorf("%s is not set → see docs/reference/config.md for its meaning", path)
				}
				return unknownKey(path)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"key": path.String(), "value": v})
			}
			return printValue(out, path, v)
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the key and its value as JSON")
	return cmd
}

func unknownKey(p config.Path) error {
	return fmt.Errorf("%s is not a configuration key → see docs/reference/config.md for the keys", p)
}

// unknownName explains a key that names a project, resource, command or data entry that does not
// exist, listing the ones that do.
func unknownName(c *config.Config, p config.Path) error {
	if len(p) < 2 {
		return nil
	}
	var kind string
	var known []string
	switch {
	case p[0] == "projects":
		kind, known = "project", keys(c.Projects)
	case p[0] == "resources":
		kind, known = "resource", keys(c.Resources)
	case p[0] == "commands":
		kind, known = "command", keys(c.Commands)
	case p[0] == "lanes" && p[1] == "data" && len(p) > 2:
		kind, known, p = "data entry", keys(c.Lanes.Data), p[1:]
	default:
		return nil
	}
	for _, k := range known {
		if k == p[1] {
			return nil
		}
	}
	list := "none are configured"
	if len(known) > 0 {
		list = "known: " + strings.Join(known, ", ")
	}
	return fmt.Errorf("there is no %s named %q → %s", kind, p[1], list)
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// printValue prints a string as it is (for scripts), any other leaf as TOML, and a table as TOML.
func printValue(w io.Writer, p config.Path, v any) error {
	switch x := v.(type) {
	case string:
		_, err := fmt.Fprintln(w, ui.Clean(x))
		return err
	case map[string]any:
		data, err := toml.Marshal(x)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	default:
		data, err := toml.Marshal(map[string]any{"v": v})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, strings.TrimSpace(strings.TrimPrefix(string(data), "v = ")))
		return err
	}
}

func newConfigExplainCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "explain <key>",
		Short: "Show a key's effective value and every layer that set it",
		Long: `Show a key's effective value and every layer that set it, with the file and line.

A table key explains every key under it. A setting refused because the team locked the key is
shown as refused, with the lock.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			path, err := config.ParsePath(args[0])
			if err != nil {
				return fmt.Errorf("%q is not a key: %v → write a dotted key, such as lanes.max", args[0], err)
			}
			_, c, err := loadConfig()
			if err != nil {
				return err
			}
			if err := unknownName(c, path); err != nil {
				return err
			}
			provs, known := c.Explain(path)
			if !known {
				return unknownKey(path)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if provs == nil {
					provs = []*config.Provenance{}
				}
				return writeJSON(out, map[string]any{"key": path.String(), "keys": provs})
			}
			if len(provs) == 0 {
				_, err := fmt.Fprintf(out, "%s is not set by any layer\n", path)
				return err
			}
			for i, pr := range provs {
				if i > 0 {
					fmt.Fprintln(out)
				}
				fmt.Fprintf(out, "%s = %s\n", pr.Key, tomlInline(pr.Value))
				for j := len(pr.Settings) - 1; j >= 0; j-- {
					s := pr.Settings[j]
					where := s.Pos.String()
					if s.Layer == config.LayerDefault {
						where = "built in"
					}
					status := s.Status
					if s.Lock != "" {
						status = fmt.Sprintf("refused: the team locks %q", s.Lock)
					}
					fmt.Fprintf(out, "  %-10s  %-9s  %s  (%s)\n", s.Layer, tomlInline(s.Value), where, status)
				}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the provenance as JSON")
	return cmd
}

func tomlInline(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	data, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return fmt.Sprint(v)
	}
	s := strings.TrimSpace(strings.TrimPrefix(string(data), "v = "))
	if strings.HasPrefix(s, "[v]") || strings.Contains(s, "\n") {
		return "{…}"
	}
	return s
}

func newConfigValidateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate [workspace.toml…]",
		Short: "Check the configuration, or the given team files, and report every problem",
		Long: `Check the configuration and report every problem, each with its file, line and fix.

With no arguments, it checks the workspace's layers. With files, it checks each as a team layer on
its own, which is how the examples in the repository are checked.`,
		RunE: run(func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			type result struct {
				File    string           `json:"file"`
				Valid   bool             `json:"valid"`
				Errors  []*config.Error  `json:"errors"`
				Refused []config.Refusal `json:"refused"`
			}
			var results []result
			check := func(file string, c *config.Config, err error) error {
				r := result{File: file, Valid: err == nil, Errors: []*config.Error{}, Refused: []config.Refusal{}}
				var cerrs config.Errors
				switch {
				case err == nil:
					r.Refused = append(r.Refused, c.Refused...)
				case errors.As(err, &cerrs):
					r.Errors = cerrs
				default:
					return err
				}
				results = append(results, r)
				return nil
			}
			if len(args) == 0 {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				ws, err := workspace.Find(cwd)
				if err != nil {
					return err
				}
				// In a lane, the lane's copy is the change being proposed, so it is the one checked.
				dir := ws.ConfigDir()
				c, err := config.Load(dir)
				if err := check(filepath.Join(dir, config.TeamFile), c, err); err != nil {
					return err
				}
			}
			for _, f := range args {
				data, err := os.ReadFile(f)
				if err != nil {
					return fmt.Errorf("%s cannot be read: %v → check the path", f, err)
				}
				c, err := config.LoadBytes(f, data)
				if err := check(f, c, err); err != nil {
					return err
				}
			}

			invalid := 0
			for _, r := range results {
				if !r.Valid {
					invalid++
				}
			}
			if asJSON {
				if err := writeJSON(out, map[string]any{"valid": invalid == 0, "results": results}); err != nil {
					return err
				}
			} else {
				for _, r := range results {
					if r.Valid {
						fmt.Fprintf(out, "%s: valid\n", r.File)
					}
					for _, e := range r.Errors {
						fmt.Fprintln(out, e.Error())
					}
					for _, ref := range r.Refused {
						fmt.Fprintln(out, "warning: "+ref.String())
					}
				}
			}
			if invalid > 0 {
				return &silentError{fmt.Errorf("%d of %d configurations are invalid", invalid, len(results))}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the results as JSON")
	return cmd
}

// silentError is a failure whose details the command has already printed: Main exits 1 and adds
// only its one-line summary to standard error, and no JSON error object.
type silentError struct{ err error }

func (e *silentError) Error() string { return e.err.Error() }
func (e *silentError) Unwrap() error { return e.err }
