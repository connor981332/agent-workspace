//go:build snapshot

package main

// The risk 1 fallback: instead of opening SQLite, the hot path reads a small per-session file that
// every state change rewrites (atomically, by rename).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type sessionSnapshot struct {
	Lane          string `json:"lane"`
	Generation    int    `json:"generation"`
	Notifications []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	} `json:"notifications"`
}

func readState(dir, session string) (string, int, error) {
	data, err := os.ReadFile(filepath.Join(dir, "sessions", session+".json"))
	if os.IsNotExist(err) {
		return "none", 0, nil
	} else if err != nil {
		return "", 0, err
	}
	var s sessionSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return "", 0, err
	}
	return s.Lane, len(s.Notifications), nil
}

func setupState(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		data := fmt.Sprintf(`{"lane":"%02d","generation":%d,"notifications":[{"id":%d,"body":"seat granted"}]}`, i%10+1, i, i)
		if err := os.WriteFile(filepath.Join(dir, "sessions", fmt.Sprintf("session-%02d.json", i)), []byte(data), 0o600); err != nil {
			return err
		}
	}
	return nil
}
