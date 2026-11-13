// Package audit gathers what the workspace audit starts from: suggestions that a program can make
// about a workspace configuration, and the changelog since the configuration was last designed or
// audited, with the configuration keys each item names (plans/workspace-audit.md, Phase 1). It
// only reads. The workspace audit judges and discusses; this package finds what can be found
// mechanically.
//
// It is not the readiness audit, which checks the projects (aw doctor --projects, internal/inspect).
package audit

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/runners"
	"github.com/connor981332/agent-workspace/internal/upgrade"
)

// Introduced is the aw version that added the workspace audit and [workspace] audited_with.
const Introduced = "0.3.0"

// Rules, by id. Each rule that offers an alternative cites the version whose changelog introduced
// it; a check with no such entry belongs in aw doctor as a warning, not here.
const (
	RuleAuditedWithMissing  = "audited-with-missing"
	RuleAuditedWithOld      = "audited-with-old"
	RuleDevcontainerSlot    = "devcontainer-local-slot"
	RuleDevcontainerWiring  = "devcontainer-committed-wiring"
	devcontainerAlternative = "0.2.0"
)

// Suggestion is one advisory finding about the configuration. ID is what [audit] declined names:
// the rule, followed by the project for a rule about one project.
type Suggestion struct {
	ID      string `json:"id"`
	Rule    string `json:"rule"`
	Project string `json:"project,omitempty"`
	Key     string `json:"key,omitempty"`
	File    string `json:"file,omitempty"`
	Since   string `json:"since"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

// Suggest returns the suggestions for a workspace whose owner checkout is owner, leaving out the
// ones the team declined. version is the running aw's.
func Suggest(ctx context.Context, owner string, c *config.Config, version string) []Suggestion {
	out := []Suggestion{}
	add := func(s Suggestion) {
		s.ID = s.Rule
		if s.Project != "" {
			s.ID += "-" + s.Project
		}
		if !declined(c, s) {
			out = append(out, s)
		}
	}
	stamp(c, version, add)
	for _, name := range sortedKeys(c.Projects) {
		devcontainer(ctx, owner, name, c.Projects[name], add)
	}
	return out
}

// declined reports whether the team declined a suggestion, and nothing has changed it since: its
// rule is not newer than the configuration's last audit.
func declined(c *config.Config, s Suggestion) bool {
	if _, ok := c.Audit.Declined[s.ID]; !ok {
		return false
	}
	return c.Workspace.AuditedWith == "" || upgrade.Compare(s.Since, c.Workspace.AuditedWith) <= 0
}

func stamp(c *config.Config, version string, add func(Suggestion)) {
	at := c.Workspace.AuditedWith
	if at == "" {
		add(Suggestion{Rule: RuleAuditedWithMissing, Key: "workspace.audited_with", Since: Introduced,
			Message: "this configuration does not say which aw version designed or last audited it",
			Fix:     "aw audit compares it with this aw and records the version in [workspace] audited_with"})
		return
	}
	running, ok := minor(version)
	if !ok {
		return // a development build: it has no place in the changelog's order
	}
	core, _, _ := strings.Cut(at, "-")
	if last, ok := minor(core); ok && less(last, running) {
		since := fmt.Sprintf("%d.%d.0", running[0], running[1])
		n := len(upgrade.Entries(at, ""))
		add(Suggestion{Rule: RuleAuditedWithOld, Key: "workspace.audited_with", Since: since,
			Message: fmt.Sprintf("this configuration was last audited with aw %s; this is aw %s, and the changelog has %d items since", at, version, n),
			Fix:     "aw changelog lists them, and aw audit walks through what applies to this workspace"})
	}
}

func devcontainer(ctx context.Context, owner, name string, p *config.Project, add func(Suggestion)) {
	if p.Runner != "devcontainer" || p.LocalCompose != "" {
		return
	}
	dir := filepath.Join(owner, p.Directory)
	j, err := runners.Validate(dir, "")
	if err != nil {
		return // aw doctor reports it
	}
	files := runners.ComposeFiles(j)
	for _, f := range files {
		if f == runners.Fragment || filepath.IsAbs(f) || strings.Contains(f, "${") || strings.Contains(f, "..") {
			continue
		}
		if ignored, _ := run.In(dir).OK(ctx, "check-ignore", "-q", "--", filepath.Join(".devcontainer", f)); ignored {
			add(Suggestion{Rule: RuleDevcontainerSlot, Project: name, Key: "projects." + name + ".local_compose",
				File: filepath.Join(p.Directory, ".devcontainer", "devcontainer.json"), Since: devcontainerAlternative,
				Message: fmt.Sprintf("devcontainer.json lists %s, a gitignored local Compose file, so aw could write its mount and seat ports there and VS Code would need no separate aw configuration", f),
				Fix:     fmt.Sprintf("set projects.%s.local_compose = %q, if devcontainer.json's initializeCommand creates it when it is missing", name, f)})
			return
		}
	}
	if slices.Contains(files, runners.Fragment) {
		add(Suggestion{Rule: RuleDevcontainerWiring, Project: name,
			File: filepath.Join(p.Directory, ".devcontainer", "devcontainer.json"), Since: devcontainerAlternative,
			Message: "devcontainer.json lists " + runners.Fragment + ", aw's committed wiring, which aw no longer needs: it can generate " + runners.GeneratedConfig + " instead, and the project carries nothing of aw's",
			Fix:     "remove " + runners.Fragment + " and aw's claim step from devcontainer.json, and select the (aw) configuration in VS Code; or keep it, and decline this suggestion"})
	}
}

// Change is a changelog item, with the configuration keys it names.
type Change struct {
	upgrade.Entry
	Keys []string `json:"keys,omitempty"`
}

var codeSpan = regexp.MustCompile("`([^`]+)`")

// Changelog returns this aw's changelog items after the version from (all of them when it is
// empty), newest first, each with the configuration keys it names in code spans:
// `projects.<name>.local_compose`, or `lanes.max = 4`.
func Changelog(from string) []Change {
	out := []Change{}
	for _, e := range upgrade.Entries(from, "") {
		ch := Change{Entry: e}
		for _, m := range codeSpan.FindAllStringSubmatch(e.Text, -1) {
			if key := keyIn(m[1]); key != "" && !slices.Contains(ch.Keys, key) {
				ch.Keys = append(ch.Keys, key)
			}
		}
		out = append(out, ch)
	}
	return out
}

// keyIn returns the configuration key a code span names, in any of the ways the changelog writes
// one: projects.<name>.env, [projects.<name>.env], [workspace] generated, lanes.max = 4, or a key's
// own name when only one key has it, such as local_compose. Anything else is "".
func keyIn(span string) string {
	span, _, _ = strings.Cut(span, "=")
	span = strings.TrimSpace(span)
	if rest, ok := strings.CutPrefix(span, "["); ok {
		table, after, ok := strings.Cut(rest, "]")
		if !ok {
			return ""
		}
		span = table
		if word := strings.Fields(after); len(word) > 0 {
			span += "." + word[0]
		}
	}
	if strings.Contains(span, ".") {
		if config.KnownKey(span) {
			return span
		}
		return ""
	}
	if keys := config.KeysNamed(span); len(keys) == 1 {
		return keys[0]
	}
	return ""
}

// minor is a release version's major and minor numbers. A pre-release or development build has none.
func minor(v string) ([2]int, bool) {
	if !config.VersionRE.MatchString(v) || strings.Contains(v, "-") {
		return [2]int{}, false
	}
	parts := strings.SplitN(v, ".", 3)
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	return [2]int{a, b}, true
}

func less(a, b [2]int) bool { return a[0] < b[0] || a[0] == b[0] && a[1] < b[1] }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
