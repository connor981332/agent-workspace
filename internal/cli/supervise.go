package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"text/template"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/ui"
)

var (
	systemdUnit = template.Must(template.New("unit").Parse(`[Unit]
Description=Agent Workspace supervisor for {{.Namespace}}

[Service]
WorkingDirectory={{.Owner}}
ExecStart={{.Exe}} supervise
Restart=on-failure

[Install]
WantedBy=default.target
`))
	launchdPlist = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{.Label}}</string>
  <key>WorkingDirectory</key><string>{{.Owner}}</string>
  <key>ProgramArguments</key><array><string>{{.Exe}}</string><string>supervise</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
`))
)

// supervise once runs what any state-changing command would: recovering dead holders and applying
// idle expiry and maximum hold. It has no authority the CLI lacks.
func superviseOnce(ctx context.Context) error {
	w, err := openForWrite()
	if err != nil {
		return err
	}
	defer w.Close()
	return newBroker(w).Recover(ctx)
}

func newSuperviseCmd() *cobra.Command {
	var once, install, uninstall bool
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "supervise [--install | --uninstall | --once]",
		Short: "Optionally keep expiry and eviction on time, as a user service",
		Long: `Run what any state-changing command already does, on a timer, so that expiry and eviction happen on
time rather than at the next command. It is optional, and has no authority the command line lacks.
--install writes a systemd user unit, or a launchd agent on macOS, and prints how to start it.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if install || uninstall {
				path, start, stop := servicePaths(c)
				if uninstall {
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						return err
					}
					fmt.Fprintf(out, "Removed %s. To stop it now: %s\n", ui.Clean(path), stop)
					return nil
				}
				exe := filepath.Join(ws.Owner, "bin", "aw")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				f, err := os.Create(path)
				if err != nil {
					return err
				}
				defer f.Close()
				t := systemdUnit
				if runtime.GOOS == "darwin" {
					t = launchdPlist
				}
				if err := t.Execute(f, map[string]string{"Namespace": c.Workspace.Namespace, "Owner": ws.Owner, "Exe": exe, "Label": "com.agent-workspace." + c.Workspace.Namespace}); err != nil {
					return err
				}
				fmt.Fprintf(out, "Wrote %s. To start it: %s\n", ui.Clean(path), start)
				return nil
			}
			if once {
				return superviseOnce(cmd.Context())
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			tick := time.NewTicker(every)
			defer tick.Stop()
			for {
				if err := superviseOnce(ctx); err != nil && ctx.Err() == nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "aw supervise: %v\n", err)
				}
				select {
				case <-ctx.Done():
					return nil
				case <-tick.C:
				}
			}
		}),
	}
	cmd.Flags().BoolVar(&once, "once", false, "do one pass, and exit")
	cmd.Flags().BoolVar(&install, "install", false, "write the user service")
	cmd.Flags().BoolVar(&uninstall, "uninstall", false, "remove the user service")
	cmd.Flags().DurationVar(&every, "every", 30*time.Second, "how often")
	return cmd
}

func servicePaths(c *config.Config) (path, start, stop string) {
	name := "aw-" + c.Workspace.Namespace
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		label := "com.agent-workspace." + c.Workspace.Namespace
		path = filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		return path, "launchctl load " + strconv.Quote(path), "launchctl unload " + strconv.Quote(path)
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", name+".service"), "systemctl --user enable --now " + name, "systemctl --user disable --now " + name
}
