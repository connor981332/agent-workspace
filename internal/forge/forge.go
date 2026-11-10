// Package forge reads change-request evidence from GitHub (through gh) and GitLab (through glab),
// read-only. Cleanup treats "merged" as permission to delete work, so every path fails closed:
// anything short of one exact match on the lane's head reports unknown or absent, and the lane is
// kept. Forks are excluded, because a fork's branch can reuse a lane branch's name. (Ported from
// the earlier Python implementation's forge.py.)
package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Availability values.
const (
	Verified     = "verified"
	Ambiguous    = "ambiguous"
	HeadMismatch = "source head mismatch"
	Absent       = "confirmed absent"
)

// Change is what a forge says about a branch at a head.
type Change struct {
	Availability string `json:"availability"`
	Project      string `json:"project,omitempty"`
	IID          int    `json:"iid,omitempty"`
	URL          string `json:"url,omitempty"`
	SourceHead   string `json:"source_head,omitempty"`
	State        string `json:"state,omitempty"` // opened, merged or closed
	Draft        bool   `json:"draft,omitempty"`
}

// Merged reports whether the change proves the head merged.
func (c Change) Merged() bool { return c.Availability == Verified && c.State == "merged" }

// Identity is a repository on a forge, and the branch its changes target.
type Identity struct {
	Host    string `json:"host"`
	Project string `json:"project"` // owner/repo, or group/subgroup/repo
	Target  string `json:"target"`  // the base branch, e.g. main
}

var (
	scpLike = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+?)(?:\.git)?/?$`)
	urlLike = regexp.MustCompile(`^(?:ssh|https?|git)://(?:[^@/]+@)?([^/:]+)(?::\d+)?/(.+?)(?:\.git)?/?$`)
)

// ParseRemote reads a remote URL: git@host:o/r.git, ssh://git@host/o/r, https://host/o/r.git.
// A local path has no forge.
func ParseRemote(raw string) (host, project string, ok bool) {
	raw = strings.TrimSpace(raw)
	if m := urlLike.FindStringSubmatch(raw); m != nil {
		return strings.ToLower(m[1]), m[2], strings.Contains(m[2], "/")
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, "file:") {
		return "", "", false
	}
	if m := scpLike.FindStringSubmatch(raw); m != nil {
		return strings.ToLower(m[1]), m[2], strings.Contains(m[2], "/")
	}
	return "", "", false
}

// Client queries one forge within a deadline, with a cache.
type Client struct {
	kind     string // github or gitlab
	deadline time.Time
	cache    map[string]any
	calls    int
}

// For chooses an adapter. "auto" recognizes only hosts that name the forge; any other host has no
// adapter, so evidence stays unknown rather than guessing an API.
func For(setting, host string, deadline time.Time) *Client {
	host = strings.ToLower(host)
	switch {
	case setting == "none" || host == "":
		return nil
	case setting == "github" || setting == "auto" && strings.Contains(host, "github"):
		return &Client{kind: "github", deadline: deadline, cache: map[string]any{}}
	case setting == "gitlab" || setting == "auto" && strings.Contains(host, "gitlab"):
		return &Client{kind: "gitlab", deadline: deadline, cache: map[string]any{}}
	}
	return nil
}

// Kind is github or gitlab.
func (c *Client) Kind() string { return c.kind }

const maxCalls = 64

// call runs the CLI and decodes its JSON output, or returns nil on any failure.
func (c *Client) call(ctx context.Context, args []string) any {
	key := strings.Join(args, "\x00")
	if v, ok := c.cache[key]; ok {
		return v
	}
	if c.calls >= maxCalls {
		return nil
	}
	left := time.Until(c.deadline)
	if left <= 0 {
		return nil
	}
	c.calls++
	ctx, cancel := context.WithTimeout(ctx, min(5*time.Second, left))
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.WaitDelay = 500 * time.Millisecond // a child the CLI left behind must not hold its output open
	out, err := cmd.Output()
	var v any
	if err == nil && json.Unmarshal(out, &v) != nil {
		v = nil
	}
	if err != nil {
		v = nil
	}
	c.cache[key] = v
	return v
}

func unknown(why string) Change { return Change{Availability: "unknown (" + why + ")"} }

func single(exact []Change, matched int) Change {
	switch {
	case len(exact) == 1:
		exact[0].Availability = Verified
		return exact[0]
	case len(exact) > 1:
		return Change{Availability: Ambiguous}
	case matched > 0:
		return Change{Availability: HeadMismatch}
	}
	return Change{Availability: Absent}
}

// Find looks for the change request of branch at head.
func (c *Client) Find(ctx context.Context, id Identity, branch, head string) Change {
	if c.kind == "github" {
		return c.github(ctx, id, branch, head)
	}
	return c.gitlab(ctx, id, branch, head)
}

func (c *Client) github(ctx context.Context, id Identity, branch, head string) Change {
	owner, _, _ := strings.Cut(id.Project, "/")
	var matches []map[string]any
	for page := 1; ; page++ {
		if page > 5 {
			return unknown("pagination limit")
		}
		path := fmt.Sprintf("repos/%s/pulls?state=all&head=%s&base=%s&per_page=100&page=%d",
			id.Project, url.QueryEscape(owner+":"+branch), url.QueryEscape(id.Target), page)
		data, ok := c.call(ctx, []string{"gh", "api", "--hostname", id.Host, path}).([]any)
		if !ok {
			return unknown("GitHub unavailable")
		}
		for _, item := range data {
			p, ok := item.(map[string]any)
			if ok && str(p, "head", "ref") == branch && str(p, "base", "ref") == id.Target {
				matches = append(matches, p)
			}
		}
		if len(data) < 100 {
			break
		}
	}
	var exact []Change
	for _, p := range matches {
		sameRepo := str(p, "head", "repo", "full_name") == id.Project && str(p, "base", "repo", "full_name") == id.Project
		state := ""
		switch str(p, "state") {
		case "open":
			state = "opened"
		case "closed":
			state = "closed"
			if p["merged_at"] != nil && p["merged_at"] != "" {
				state = "merged"
			}
		}
		n, isInt := p["number"].(float64)
		if sameRepo && state != "" && str(p, "head", "sha") == head && isInt && n == float64(int(n)) {
			draft, _ := p["draft"].(bool)
			exact = append(exact, Change{Project: id.Project, IID: int(n), URL: str(p, "html_url"), SourceHead: head, State: state, Draft: draft})
		}
	}
	return single(exact, len(matches))
}

func (c *Client) gitlab(ctx context.Context, id Identity, branch, head string) Change {
	base := "projects/" + url.PathEscape(id.Project)
	api := func(endpoint string) any {
		path := base
		if endpoint != "" {
			path += "/" + endpoint
		}
		return c.call(ctx, []string{"glab", "api", "--hostname", id.Host, path})
	}
	var matches []map[string]any
	for page := 1; ; page++ {
		if page > 5 {
			return unknown("pagination limit")
		}
		data, ok := api(fmt.Sprintf("merge_requests?state=all&scope=all&source_branch=%s&per_page=100&page=%d", url.QueryEscape(branch), page)).([]any)
		if !ok {
			return unknown("GitLab unavailable")
		}
		for _, item := range data {
			m, ok := item.(map[string]any)
			if ok && str(m, "source_branch") == branch && str(m, "target_branch") == id.Target {
				matches = append(matches, m)
			}
		}
		if len(data) < 100 {
			break
		}
	}
	var exact []Change
	for _, m := range matches {
		project, ok := api("").(map[string]any)
		if !ok || m["source_project_id"] != project["id"] || m["target_project_id"] != project["id"] {
			continue
		}
		iid, _ := m["iid"].(float64)
		detail, ok := api(fmt.Sprintf("merge_requests/%d", int(iid))).(map[string]any)
		if !ok {
			return unknown("MR detail unavailable")
		}
		sha := str(detail, "diff_refs", "head_sha")
		if sha == "" {
			sha = str(detail, "sha")
		}
		if sha == head {
			draft, _ := detail["draft"].(bool)
			exact = append(exact, Change{Project: id.Project, IID: int(iid), URL: str(detail, "web_url"), SourceHead: sha, State: str(detail, "state"), Draft: draft})
		}
	}
	return single(exact, len(matches))
}

// str reads a string at a path of object keys, or "".
func str(m map[string]any, keys ...string) string {
	var v any = m
	for _, k := range keys {
		obj, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = obj[k]
	}
	s, _ := v.(string)
	return s
}

// ErrNoForge means no adapter applies.
var ErrNoForge = errors.New("no forge")

// Open opens a change request and returns its URL. Everything else in this package only reads; this
// runs when a change set is opened, which the publication policy has already allowed.
func (c *Client) Open(ctx context.Context, id Identity, branch, title, body string) (string, error) {
	args := []string{"pr", "create", "--repo", id.Project, "--head", branch, "--base", id.Target, "--title", title, "--body", body}
	tool := "gh"
	if c.kind == "gitlab" {
		tool, args = "glab", []string{"mr", "create", "--repo", id.Project, "--source-branch", branch, "--target-branch", id.Target, "--title", title, "--description", body, "--yes"}
	}
	out, err := exec.CommandContext(ctx, tool, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s could not open a merge request for %s: %w", tool, id.Project, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Describe replaces a change request's description.
func (c *Client) Describe(ctx context.Context, id Identity, url, branch, body string) error {
	args := []string{"pr", "edit", url, "--body", body}
	tool := "gh"
	if c.kind == "gitlab" {
		tool, args = "glab", []string{"mr", "update", branch, "--repo", id.Project, "--description", body, "--yes"}
	}
	if err := exec.CommandContext(ctx, tool, args...).Run(); err != nil {
		return fmt.Errorf("%s could not link the merge request in %s: %w", tool, id.Project, err)
	}
	return nil
}
