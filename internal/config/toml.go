package config

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Path is a key path: table and key names, with "[n]" segments for array elements.
type Path []string

func (p Path) child(s string) Path { return append(p[:len(p):len(p)], s) }

func (p Path) index(i int) Path { return p.child("[" + strconv.Itoa(i) + "]") }

// String formats a path as TOML would write it: projects.web.servers[0].port, quoting segments that
// are not bare keys.
func (p Path) String() string {
	var b strings.Builder
	for i, s := range p {
		if strings.HasPrefix(s, "[") {
			b.WriteString(s)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		if bareKey(s) {
			b.WriteString(s)
		} else {
			b.WriteString(strconv.Quote(s))
		}
	}
	return b.String()
}

func (p Path) key() string { return strings.Join(p, "\x00") }

func bareKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// ParsePath parses a dotted key as a user types it: lanes.max, projects."my app".runner.
func ParsePath(s string) (Path, error) {
	if s == "" {
		return nil, errors.New("empty key")
	}
	var p Path
	for s != "" {
		var seg string
		if s[0] == '"' {
			end := 1
			for end < len(s) && (s[end] != '"' || s[end-1] == '\\') {
				end++
			}
			if end == len(s) {
				return nil, fmt.Errorf("unclosed quote in key")
			}
			unq, err := strconv.Unquote(s[:end+1])
			if err != nil {
				return nil, fmt.Errorf("bad quoted key %s", s[:end+1])
			}
			seg, s = unq, s[end+1:]
		} else {
			end := strings.IndexAny(s, ".[")
			if end < 0 {
				end = len(s)
			}
			seg, s = s[:end], s[end:]
			if !bareKey(seg) {
				return nil, fmt.Errorf("%q is not a valid key segment", seg)
			}
		}
		p = append(p, seg)
		for strings.HasPrefix(s, "[") {
			end := strings.IndexByte(s, ']')
			if end < 0 {
				return nil, errors.New("unclosed [ in key")
			}
			if _, err := strconv.Atoi(s[1:end]); err != nil {
				return nil, fmt.Errorf("%q is not an index", s[1:end])
			}
			p = append(p, s[:end+1])
			s = s[end+1:]
		}
		if s == "" {
			break
		}
		if s[0] != '.' || len(s) == 1 {
			return nil, fmt.Errorf("unexpected %q in key", s)
		}
		s = s[1:]
	}
	return p, nil
}

// Pos is a position in a configuration file.
type Pos struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

func (p Pos) String() string {
	if p.Line == 0 {
		return p.File
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// doc is one parsed TOML file: its values, and the position of every key in it.
type doc struct {
	file   string
	values map[string]any
	pos    map[string]Pos // by Path.key()
}

func (d *doc) at(p Path) Pos {
	for q := p; len(q) > 0; q = q[:len(q)-1] {
		if pos, ok := d.pos[q.key()]; ok {
			return pos
		}
	}
	return Pos{File: d.file}
}

// parseTOML decodes a file. TOML syntax errors are returned as *Error with their position.
func parseTOML(file string, data []byte) (*doc, error) {
	d := &doc{file: file, values: map[string]any{}, pos: map[string]Pos{}}
	if err := toml.Unmarshal(data, &d.values); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, col := de.Position()
			return nil, &Error{Pos: Pos{File: file, Line: line, Col: col}, Msg: "invalid TOML: " + strings.TrimPrefix(de.Error(), "toml: "),
				Fix: "correct the TOML syntax at that position"}
		}
		return nil, &Error{Pos: Pos{File: file}, Msg: "invalid TOML: " + err.Error(), Fix: "correct the TOML syntax"}
	}

	var p unstable.Parser
	p.Reset(data)
	shape := func(n *unstable.Node) Pos {
		s := p.Shape(n.Raw)
		return Pos{File: file, Line: s.Start.Line, Col: s.Start.Column}
	}
	record := func(path Path, pos Pos) {
		if _, ok := d.pos[path.key()]; !ok {
			d.pos[path.key()] = pos
		}
	}
	keyPath := func(prefix Path, n *unstable.Node) (Path, Pos) {
		path := prefix
		var first Pos
		it := n.Key()
		for i := 0; it.Next(); i++ {
			k := it.Node()
			path = path.child(string(k.Data))
			if i == 0 {
				first = shape(k)
			}
			record(path, shape(k))
		}
		return path, first
	}
	arrayCount := map[string]int{}
	var walkValue func(path Path, v *unstable.Node)
	walkValue = func(path Path, v *unstable.Node) {
		switch v.Kind {
		case unstable.InlineTable:
			it := v.Children()
			for it.Next() {
				kv := it.Node()
				kp, _ := keyPath(path, kv)
				walkValue(kp, kv.Value())
			}
		case unstable.Array:
			it := v.Children()
			for i := 0; it.Next(); i++ {
				el := it.Node()
				ep := path.index(i)
				record(ep, shape(el))
				walkValue(ep, el)
			}
		}
	}

	var table Path
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Table:
			table, _ = keyPath(nil, e)
		case unstable.ArrayTable:
			base, pos := keyPath(nil, e)
			n := arrayCount[base.key()]
			arrayCount[base.key()] = n + 1
			table = base.index(n)
			record(table, pos)
		case unstable.KeyValue:
			kp, _ := keyPath(table, e)
			walkValue(kp, e.Value())
		}
	}
	if err := p.Error(); err != nil {
		return nil, &Error{Pos: Pos{File: file}, Msg: "invalid TOML: " + err.Error(), Fix: "correct the TOML syntax"}
	}
	return d, nil
}

// tomlValue renders a decoded value as TOML, for messages.
func tomlValue(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case map[string]any:
		var b bytes.Buffer
		enc := toml.NewEncoder(&b)
		enc.SetTablesInline(true)
		if err := enc.Encode(x); err == nil {
			return "{ " + strings.ReplaceAll(strings.TrimSpace(b.String()), "\n", ", ") + " }"
		}
	}
	data, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "v ="))
}
