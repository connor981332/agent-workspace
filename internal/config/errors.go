package config

import (
	"fmt"
	"sort"
	"strings"
)

// Error is one configuration problem. Every error names the file, the line and the fix (R9).
type Error struct {
	Pos Pos    `json:"position"`
	Key string `json:"key,omitempty"`
	Msg string `json:"message"`
	Fix string `json:"fix"`
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Pos.String())
	b.WriteString(": ")
	if e.Key != "" {
		b.WriteString(e.Key)
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	if e.Fix != "" {
		b.WriteString(" → ")
		b.WriteString(e.Fix)
	}
	return b.String()
}

// Errors is every problem found, in file and line order.
type Errors []*Error

func (es Errors) Error() string {
	lines := make([]string, len(es))
	for i, e := range es {
		lines[i] = e.Error()
	}
	if len(es) == 1 {
		return lines[0]
	}
	return fmt.Sprintf("%d configuration problems:\n  %s", len(es), strings.Join(lines, "\n  "))
}

func (es Errors) sorted() Errors {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i].Pos, es[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	return es
}
