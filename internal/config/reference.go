package config

import (
	"fmt"
	"strings"
)

// Reference renders the configuration reference (docs/reference/config.md) from the schema, so
// that the documentation cannot drift from what aw accepts.
func Reference() string {
	var b strings.Builder
	b.WriteString(`# Configuration reference

<!-- Generated from internal/config/schema.go by TestReference. Do not edit by hand: change the
schema, then run AW_UPDATE_GOLDEN=1 go test ./internal/config. -->

The configuration has three layers, merged in this order (requirement R6):

1. **default**: built into aw, shown in the Default column below.
2. **team**: ` + "`workspace.toml`" + ` in the owner checkout, committed.
3. **individual**: ` + "`~/.config/agent-workspace/<namespace>.toml`" + ` (or under ` + "`$XDG_CONFIG_HOME`" + `), personal.

Tables merge key by key; every other value, arrays and lifecycle hooks included, replaces the value of
the layer before it. Keys marked *team only* may not appear in the individual layer. The team can
lock more keys with ` + "`[locks] keys`" + `; an individual setting of a locked key is refused and reported
by ` + "`aw config validate`" + ` and ` + "`aw config explain`" + ` (R7).

Every unknown key and invalid value is an error that names the file, the line and the fix (R9).
` + "`aw config get <key>`" + ` prints a value, and ` + "`aw config explain <key>`" + ` shows every layer that set
it (R8). Durations are written as strings: ` + "`\"30s\"`, `\"15m\"`, `\"2h\"`, `\"14d\"`" + `.

The configuration schema version is ` + fmt.Sprint(SchemaVersion) + ` (` + "`[workspace] schema`" + `).
`)
	for _, k := range schema.order {
		renderTable(&b, Path{k}, schema.fields[k], 2)
	}
	return b.String()
}

func renderTable(b *strings.Builder, p Path, n *node, level int) {
	header := "[" + p.String() + "]"
	name := p.String()
	if n.kind == kMap {
		header = "[" + p.String() + ".<name>]"
		name = p.String() + ".<name>"
	}
	fmt.Fprintf(b, "\n%s `%s`\n\n", strings.Repeat("#", level), header)
	if n.doc != "" {
		fmt.Fprintf(b, "%s\n\n", n.doc)
	}
	t := n
	if n.kind == kMap {
		t = n.elem
		if t.kind != kTable {
			fmt.Fprintf(b, "Each value: %s.\n", leafType(t))
			return
		}
		fmt.Fprintf(b, "`<name>`: lowercase letters, digits and hyphens, starting with a letter.\n\n")
	}
	var nested []string
	rows := 0
	for _, k := range t.order {
		f := t.fields[k]
		if f.kind == kTable || (f.kind == kMap && f.elem.kind == kTable) {
			nested = append(nested, k)
			continue
		}
		if rows == 0 {
			b.WriteString("| Key | Type | Default | Meaning |\n|---|---|---|---|\n")
		}
		rows++
		def := "—"
		if f.def != nil {
			def = "`" + tomlValue(f.def) + "`"
		}
		doc := f.doc
		if f.team {
			doc += " *Team only.*"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", k, leafType(f), def, strings.ReplaceAll(doc, "|", `\|`))
	}
	for _, k := range nested {
		renderTable(b, splitName(name).child(k), t.fields[k], min(level+1, 4))
	}
}

func splitName(s string) Path {
	return Path(strings.Split(s, "."))
}

func leafType(n *node) string {
	switch n.kind {
	case kString:
		if len(n.enum) > 0 {
			return "one of " + strings.Join(quoteEach(n.enum), ", ")
		}
		return "string"
	case kInt:
		switch {
		case n.min != nil && n.max != nil:
			return fmt.Sprintf("integer, %d to %d", *n.min, *n.max)
		case n.min != nil:
			return fmt.Sprintf("integer, at least %d", *n.min)
		}
		return "integer"
	case kBool:
		return "boolean"
	case kDuration:
		return "duration"
	case kStringList:
		return "array of strings"
	case kHook:
		return "command string, or `{ run, timeout, needs }`"
	case kMap:
		return "table of " + leafType(n.elem)
	}
	return "table"
}

func quoteEach(vs []string) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = "`\"" + v + "\"`"
	}
	return out
}
