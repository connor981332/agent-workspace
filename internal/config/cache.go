package config

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The compiled cache (architecture.md): the merged configuration with its provenance, keyed by the
// metadata of its source files and by the binary's version. Hooks read it instead of merging three
// TOML layers on every call. A cache that does not match is ignored, never trusted.
//
// Format: "awcfg1\n", a little-endian uint32 header length, a JSON header, then a gob body.

const cacheMagic = "awcfg1\n"

type cacheHeader struct {
	Version string   `json:"version"`
	Sources []Source `json:"sources"`
}

func init() {
	gob.Register(map[string]any{})
	gob.Register([]any{})
}

// WriteCache writes c to path atomically, for the binary of the given version.
func WriteCache(path, version string, c *Config) error {
	hdr, err := json.Marshal(cacheHeader{Version: version, Sources: c.Sources})
	if err != nil {
		return err
	}
	var body bytes.Buffer
	if err := gob.NewEncoder(&body).Encode(c); err != nil {
		return fmt.Errorf("encoding the config cache: %w", err)
	}
	out := append([]byte(cacheMagic), binary.LittleEndian.AppendUint32(nil, uint32(len(hdr)))...)
	out = append(append(out, hdr...), body.Bytes()...)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ErrStale means the cache is missing, from another version, or older than its sources.
var ErrStale = errors.New("config cache is stale")

// ReadCache returns the cached configuration when every source still matches, else ErrStale.
func ReadCache(path, version string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrStale
	}
	rest, ok := bytes.CutPrefix(data, []byte(cacheMagic))
	if !ok || len(rest) < 4 {
		return nil, ErrStale
	}
	n := binary.LittleEndian.Uint32(rest)
	if int(n) > len(rest)-4 {
		return nil, ErrStale
	}
	var hdr cacheHeader
	if err := json.Unmarshal(rest[4:4+n], &hdr); err != nil || hdr.Version != version {
		return nil, ErrStale
	}
	for _, s := range hdr.Sources {
		if sourceOf(s.Path) != s {
			return nil, ErrStale
		}
	}
	var c Config
	if err := gob.NewDecoder(bytes.NewReader(rest[4+n:])).Decode(&c); err != nil {
		return nil, ErrStale
	}
	c.Sources = hdr.Sources
	// The individual file's path depends on the environment (XDG_CONFIG_HOME, HOME).
	if len(c.Sources) > 1 && c.Sources[1].Path != IndividualFile(c.Workspace.Namespace) {
		return nil, ErrStale
	}
	return &c, nil
}
