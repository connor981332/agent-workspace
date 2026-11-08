// Package launcher holds the bin/aw launcher template (distribution.md, "The launcher") and the
// code that pins a workspace to a release: reading a release's checksums and writing .aw/version
// and .aw/lock. It uses the network, so it is never on the hot path.
package launcher

import (
	"bufio"
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Script is the launcher, written to <workspace>/bin/aw.
//
//go:embed launcher.sh
var Script []byte

// Paths relative to the owner checkout.
const (
	ScriptPath  = "bin/aw"
	VersionPath = ".aw/version"
	LockPath    = ".aw/lock"
)

// Platforms are the release platforms, as <os>_<arch>.
var Platforms = []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"}

var exactRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$`)

// Exact reports whether v is an exact version that can be pinned (no leading v).
func Exact(v string) bool { return exactRE.MatchString(v) }

// Asset is a release asset's name.
func Asset(version, platform string) string { return "aw_" + version + "_" + platform }

// DefaultMirror is where releases are downloaded from when AW_MIRROR is not set.
func DefaultMirror(version string) string {
	return "https://github.com/connor981332/agent-workspace/releases/download/v" + version
}

// Mirror returns AW_MIRROR, or the release's download location.
func Mirror(version string) string {
	if m := os.Getenv("AW_MIRROR"); m != "" {
		return m
	}
	return DefaultMirror(version)
}

// Lock returns the lines of the release's checksums.txt for version, which become .aw/lock, and
// the platforms they cover. The mirror is a directory or an http(s) URL.
func Lock(mirror, version string) ([]byte, []string, error) {
	data, err := fetch(mirror, "checksums.txt")
	if err != nil {
		return nil, nil, err
	}
	var out bytes.Buffer
	var platforms []string
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) != 2 || len(fields[0]) != 64 {
			continue
		}
		for _, p := range Platforms {
			if fields[1] == Asset(version, p) {
				fmt.Fprintf(&out, "%s  %s\n", fields[0], fields[1])
				platforms = append(platforms, p)
			}
		}
	}
	if len(platforms) == 0 {
		return nil, nil, fmt.Errorf("%s/checksums.txt lists no aw %s binaries → check that the release exists, or set AW_MIRROR", mirror, version)
	}
	return out.Bytes(), platforms, nil
}

func fetch(mirror, name string) ([]byte, error) {
	if strings.HasPrefix(mirror, "/") {
		data, err := os.ReadFile(filepath.Join(mirror, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s from the mirror %s: %v → check AW_MIRROR", name, mirror, err)
		}
		return data, nil
	}
	if !strings.HasPrefix(mirror, "https://") && !strings.HasPrefix(mirror, "http://") {
		return nil, fmt.Errorf("AW_MIRROR %q is neither an absolute directory nor an http(s) URL", mirror)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(mirror + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("downloading %s/%s: %v → check the network, or set AW_MIRROR", mirror, name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s/%s: %s → check that the release exists", mirror, name, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// ErrNotPinnable means this binary's version cannot be pinned (a development build).
var ErrNotPinnable = errors.New("not an exact release version")
