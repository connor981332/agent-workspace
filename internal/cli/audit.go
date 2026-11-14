package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/audit"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

func newAuditCmd() *cobra.Command {
	var dropOtherHooks bool
	var harness, focus string
	cmd := &cobra.Command{
		Use:   "audit [--focus text] [--harness h] [--without-other-hooks]",
		Short: "Talk through this workspace's configuration with an agent, against the repositories and what this aw can do",
		Long: `Start an agent that audits the workspace's configuration with you: what has changed in the
repositories since it was written, what aw can do now that it could not then, and what aw doctor
suggests. The agent opens with a short brief, ranked by what each change would gain, and then
discusses it with you. It changes nothing until you agree, and then only what you agreed.

It works in a lane of its own, as aw onboard does, so its changes go on the lane's branch and
through a merge request. It may run aw rehearse there without a permission prompt. In a workspace
kept as plain files it works in the owner checkout as a pair session.

It records the aw version in [workspace] audited_with, so the next audit starts from there, and
suggestions you decline in [audit] declined, so they are not raised again.

--focus starts the conversation with your question, such as --focus "should api move to a
devcontainer?".

This audits the workspace's configuration. To find what the projects themselves must change to
work in lanes, use aw doctor --projects and the readiness-audit skill.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			return startConfigAgent(cmd, auditAgent(focus, harness, dropOtherHooks))
		}),
	}
	cmd.Flags().StringVar(&focus, "focus", "", "a question to start the conversation with")
	cmd.Flags().StringVar(&harness, "harness", "", "the harness to launch")
	cmd.Flags().BoolVar(&dropOtherHooks, "without-other-hooks", false, "turn off hooks that are not aw's in the audit's lane")
	return cmd
}

// maxChangelogPrompt is how much of the changelog the audit's prompt carries. The agent reads the
// rest with aw changelog.
const maxChangelogPrompt = 6 << 10

func auditAgent(focus, harness string, dropOtherHooks bool) configAgent {
	return configAgent{
		name: "aw audit", purpose: lanes.PurposeAudit, harness: harness, dropOtherHooks: dropOtherHooks,
		prompt: func(ws *workspace.Workspace, c *config.Config) string {
			return auditPrompt(ws, c, focus)
		},
	}
}

// auditPrompt is the audit agent's task, with the facts it starts from.
func auditPrompt(ws *workspace.Workspace, c *config.Config, focus string) string {
	var b strings.Builder
	b.WriteString("Use the aw-workspace-audit skill to audit this workspace's configuration with the developer. " +
		"Open with your brief, then wait for them: this is a conversation, and you change nothing until they agree.")
	if focus = strings.TrimSpace(focus); focus != "" {
		fmt.Fprintf(&b, "\n\nThe developer starts with this question, so begin there: %s", ui.Clean(focus))
	}
	since := c.Workspace.AuditedWith
	if since == "" {
		fmt.Fprintf(&b, "\n\nThis configuration does not say which aw designed it, so any of aw's changes may be new to it. This is aw %s. Read bin/aw changelog --json, and weigh what touches keys or features this workspace uses or could use.", frameworkVersion())
	} else {
		changes := audit.Changelog(since)
		fmt.Fprintf(&b, "\n\nThis configuration was last designed or audited with aw %s. This is aw %s.", since, frameworkVersion())
		if len(changes) == 0 {
			b.WriteString(" Nothing in aw's changelog has changed since.")
		} else {
			fmt.Fprintf(&b, " What changed in aw since %s, newest first (bin/aw changelog --json lists the configuration keys each item names):\n", since)
			used := 0
			for i, ch := range changes {
				line := "- " + ch.Version + ": " + ch.Text + "\n"
				if ch.Breaking {
					line = "- " + ch.Version + ", breaking: " + ch.Text + "\n"
				}
				if used+len(line) > maxChangelogPrompt {
					fmt.Fprintf(&b, "- and %d more: read them with bin/aw changelog\n", len(changes)-i)
					break
				}
				b.WriteString(line)
				used += len(line)
			}
		}
	}
	if suggestions := audit.Suggest(context.Background(), ws.Owner, c, frameworkVersion()); len(suggestions) > 0 {
		b.WriteString("\n\naw's own suggestions for this configuration (bin/aw doctor --json explains each; a declined one goes in [audit] declined by its id):\n")
		for _, s := range suggestions {
			fmt.Fprintf(&b, "- %s: %s → %s\n", s.ID, s.Message, s.Fix)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
