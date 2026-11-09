// Package extproto is the client of the extension protocol (architecture.md, "Extension protocol"):
// aw runs an executable with a verb, writes a JSON request to its standard input, and reads one JSON
// object from its standard output.
package extproto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Version is the protocol version aw speaks.
const Version = 1

// Timeout is how long an extension may take.
const Timeout = time.Minute

// Call runs command with the verb, and returns the fields of its reply. The request gains the
// protocol version. A non-zero exit, unparseable output, a timeout or {"ok": false} is an error.
func Call(ctx context.Context, dir, command, verb string, request map[string]any) (map[string]any, error) {
	return CallWithin(ctx, Timeout, dir, command, verb, request)
}

// CallWithin is Call with its own time limit.
func CallWithin(ctx context.Context, limit time.Duration, dir, command, verb string, request map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	body := map[string]any{"protocol": Version}
	for k, v := range request {
		body[k] = v
	}
	input, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	c := exec.CommandContext(ctx, "sh", "-c", command+` "$@"`, "sh", verb)
	c.Dir, c.Stdin = dir, bytes.NewReader(input)
	c.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s %s took longer than %s", command, verb, limit)
		}
		return nil, fmt.Errorf("%s %s failed: %v: %s", command, verb, err, strings.TrimSpace(stderr.String()))
	}
	var reply map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &reply); err != nil {
		return nil, fmt.Errorf("%s %s did not write one JSON object (%v)", command, verb, err)
	}
	if ok, _ := reply["ok"].(bool); !ok {
		msg, _ := reply["error"].(string)
		if fix, _ := reply["fix"].(string); fix != "" {
			msg += " → " + fix
		}
		return nil, fmt.Errorf("%s %s: %s", command, verb, msg)
	}
	return reply, nil
}
