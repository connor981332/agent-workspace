package cli

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/config"
)

// writeJSON prints v as indented JSON.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// jsonError is what a --json command prints on standard output when it fails, so that scripts and
// agents get the failure in the same format as the result.
type jsonError struct {
	Error struct {
		Message  string          `json:"message"`
		Fix      string          `json:"fix,omitempty"`
		Problems []*config.Error `json:"problems,omitempty"`
	} `json:"error"`
}

func newJSONError(err error) jsonError {
	var j jsonError
	var cerrs config.Errors
	if errors.As(err, &cerrs) {
		j.Error.Message = "the configuration is invalid"
		j.Error.Problems = cerrs
		return j
	}
	msg := err.Error()
	if m, fix, ok := strings.Cut(msg, " → "); ok {
		j.Error.Message, j.Error.Fix = m, fix
	} else {
		j.Error.Message = msg
	}
	return j
}

// wantsJSON reports whether the command that ran was given --json.
func wantsJSON(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	f := cmd.Flags().Lookup("json")
	return f != nil && f.Value.String() == "true"
}
