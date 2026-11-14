package cli

import (
	"fmt"
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/ui"
)

func newOptionsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "options",
		Short: "List the team's options, their choices, and which is yours",
		Long: `List the alternative setups the team supports ([options] in workspace.toml): each option, what it
decides, its choices and default, and the choice in effect for you. Pick another in your individual
file (~/.config/agent-workspace/<namespace>.toml):

  [choose]
  event-bus = "real"

Hooks and commands see the choice as AW_OPTION_<NAME>. A lane created earlier keeps what it was set
up with; aw setup <lane> brings it up to date where it can.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			_, c, err := loadProposedConfig(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			type choice struct {
				Name    string `json:"name"`
				Default bool   `json:"default"`
				Chosen  bool   `json:"chosen"`
			}
			type option struct {
				Name        string   `json:"name"`
				Description string   `json:"description,omitempty"`
				Chosen      string   `json:"chosen"`
				Choices     []choice `json:"choices"`
			}
			list := []option{}
			for _, name := range slices.Sorted(maps.Keys(c.Options)) {
				o := c.Options[name]
				opt := option{Name: name, Description: o.Description, Chosen: c.Chosen[name], Choices: []choice{}}
				for _, ch := range slices.Sorted(maps.Keys(o.Choices)) {
					opt.Choices = append(opt.Choices, choice{Name: ch, Default: ch == o.Default, Chosen: ch == c.Chosen[name]})
				}
				list = append(list, opt)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"options": list})
			}
			if len(list) == 0 {
				fmt.Fprintln(out, "This workspace has no options.")
				return nil
			}
			for _, o := range list {
				fmt.Fprintf(out, "%s: %s", o.Name, ui.Clean(o.Chosen))
				if o.Description != "" {
					fmt.Fprintf(out, "  (%s)", ui.Clean(o.Description))
				}
				fmt.Fprintln(out)
				for _, ch := range o.Choices {
					mark := "  "
					if ch.Chosen {
						mark = "* "
					}
					suffix := ""
					if ch.Default {
						suffix = " (default)"
					}
					fmt.Fprintf(out, "  %s%s%s\n", mark, ui.Clean(ch.Name), suffix)
				}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the options as JSON")
	return cmd
}
