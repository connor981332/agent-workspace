//go:build withcli

package main

// Links cobra, as cmd/aw does, to measure its package initialization on the hot path.
import "github.com/spf13/cobra"

var _ = &cobra.Command{Use: "aw"}
