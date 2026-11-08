package cmds

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rogpeppe/go-internal/testscript"
)

// json <file> <path> [value]
//
// file is stdout, stderr or a file. path is ".", or a sequence of ".key" and "[n]". With a value,
// the value at path must equal it: a JSON string compares as its text, anything else as its
// compact JSON (so 0, true, null, [] and {"a":1} are written as JSON). Without a value, the path
// must exist. With !, the value must differ, or the path must not exist.
func cmdJSON(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 2 && len(args) != 3 {
		ts.Fatalf("usage: [!] json <file> <path> [value]")
	}
	text := ts.ReadFile(args[0])
	var doc any
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		ts.Fatalf("json: %s is not JSON: %v\n%s", args[0], err, text)
	}
	if dec.More() {
		ts.Fatalf("json: %s holds more than one JSON value", args[0])
	}

	got, found, err := lookup(doc, args[1])
	if err != nil {
		ts.Fatalf("json: %v", err)
	}
	if len(args) == 2 {
		switch {
		case neg && found:
			ts.Fatalf("json: %s exists in %s", args[1], args[0])
		case !neg && !found:
			ts.Fatalf("json: %s not found in %s", args[1], args[0])
		}
		return
	}
	if !found {
		if neg {
			return
		}
		ts.Fatalf("json: %s not found in %s", args[1], args[0])
	}
	rendered := render(got)
	switch {
	case neg && rendered == args[2]:
		ts.Fatalf("json: %s is %s", args[1], rendered)
	case !neg && rendered != args[2]:
		ts.Fatalf("json: %s is %s, want %s", args[1], rendered, args[2])
	}
}

func render(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// lookup follows path through doc. A missing key or index is reported as not found; a step into a
// value of the wrong kind is an error in the path.
func lookup(doc any, path string) (any, bool, error) {
	if path == "." {
		return doc, true, nil
	}
	if !strings.HasPrefix(path, ".") && !strings.HasPrefix(path, "[") {
		return nil, false, fmt.Errorf("path %q must start with . or [", path)
	}
	cur := doc
	rest := path
	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			key := rest[:end]
			rest = rest[end:]
			if key == "" {
				return nil, false, fmt.Errorf("path %q has an empty key", path)
			}
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("path %q: .%s applied to %s", path, key, kind(cur))
			}
			if cur, ok = obj[key]; !ok {
				return nil, false, nil
			}
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, false, fmt.Errorf("path %q: unclosed [", path)
			}
			n, err := strconv.Atoi(rest[1:end])
			if err != nil || n < 0 {
				return nil, false, fmt.Errorf("path %q: %q is not an index", path, rest[1:end])
			}
			rest = rest[end+1:]
			arr, ok := cur.([]any)
			if !ok {
				return nil, false, fmt.Errorf("path %q: [%d] applied to %s", path, n, kind(cur))
			}
			if n >= len(arr) {
				return nil, false, nil
			}
			cur = arr[n]
		default:
			return nil, false, fmt.Errorf("path %q: unexpected %q", path, rest[0])
		}
	}
	return cur, true, nil
}

func kind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}
