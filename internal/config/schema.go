package config

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the configuration schema this binary reads and writes (R10).
const SchemaVersion = 1

// kind is the type of a schema node.
type kind int

const (
	kTable      kind = iota // fixed keys
	kMap                    // any keys, each holding elem
	kString                 // string
	kInt                    // integer
	kBool                   // boolean
	kDuration               // string such as "15s", "45m", "14d"
	kStringList             // array of strings
	kHook                   // string, or { run, timeout, needs }
	kMux                    // none, herdr, tmux, or { command }
)

// layer names where a key comes from.
const (
	LayerDefault    = "default"
	LayerTeam       = "team"
	LayerIndividual = "individual"
)

// node describes one key of the schema.
type node struct {
	kind   kind
	doc    string
	fields map[string]*node // kTable
	order  []string         // kTable: key order for documentation
	elem   *node            // kMap
	keyRE  *regexp.Regexp   // kMap: what a key must look like
	keyFix string           // kMap: how to fix a bad key
	def    any              // default value, for leaves
	enum   []string         // allowed strings
	min    *int             // integers
	max    *int
	team   bool // only the team layer may set it
	check  func(v any) (msg, fix string)
}

func table(doc string, kv ...any) *node {
	n := &node{kind: kTable, doc: doc, fields: map[string]*node{}}
	for i := 0; i < len(kv); i += 2 {
		name := kv[i].(string)
		n.fields[name] = kv[i+1].(*node)
		n.order = append(n.order, name)
	}
	return n
}

func mapOf(doc string, keyRE *regexp.Regexp, keyFix string, elem *node) *node {
	return &node{kind: kMap, doc: doc, elem: elem, keyRE: keyRE, keyFix: keyFix}
}

func str(doc string) *node      { return &node{kind: kString, doc: doc} }
func integer(doc string) *node  { return &node{kind: kInt, doc: doc} }
func boolean(doc string) *node  { return &node{kind: kBool, doc: doc} }
func duration(doc string) *node { return &node{kind: kDuration, doc: doc} }
func strList(doc string) *node  { return &node{kind: kStringList, doc: doc} }
func hook(doc string) *node     { return &node{kind: kHook, doc: doc} }
func muxNode(doc string) *node  { return &node{kind: kMux, doc: doc, def: "none"} }

func (n *node) withDefault(v any) *node { n.def = v; return n }
func (n *node) oneOf(vs ...string) *node {
	n.enum = vs
	return n
}
func (n *node) between(lo, hi int) *node { n.min, n.max = &lo, &hi; return n }
func (n *node) atLeast(lo int) *node     { n.min = &lo; return n }
func (n *node) teamOnly() *node          { n.team = true; return n }
func (n *node) checked(f func(v any) (string, string)) *node {
	n.check = f
	return n
}

var (
	capabilities       = []string{"publish", "merge", "containers", "servers"}
	publicationActions = []string{"push", "open-merge-request", "merge"}
	safeguardGroups    = []string{"git-safety", "publication", "secrets", "processes", "containers"}
)

// oneOfEach checks that every string in a list is one of the allowed values.
func oneOfEach(allowed ...string) func(v any) (string, string) {
	return func(v any) (string, string) {
		list, _ := v.([]any)
		for _, e := range list {
			if s, _ := e.(string); !slices.Contains(allowed, s) {
				return fmt.Sprintf("has %v, which is not one of them", e), "use " + strings.Join(allowed, ", ")
			}
		}
		return "", ""
	}
}

var (
	nameRE      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	acceptedRE  = regexp.MustCompile(`^[^\s/][^\s]*$`)
	namespaceRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,19}$`)
	nameFix     = "use lowercase letters, digits and hyphens, starting with a letter (at most 40 characters)"
)

// schema is the whole configuration. docs/reference/config.md is generated from it.
var schema = table("",
	"workspace", table("The workspace itself. Team layer only.",
		"name", str("Display name.").teamOnly(),
		"namespace", str("Names the state, Git metadata, environment prefixes and the individual file "+
			"(~/.config/agent-workspace/<namespace>.toml). Lowercase letters and digits, starting with a letter.").teamOnly().
			checked(func(v any) (string, string) {
				if !namespaceRE.MatchString(v.(string)) {
					return "is not a valid namespace", "use 1 to 20 lowercase letters and digits, starting with a letter, e.g. \"shop\""
				}
				return "", ""
			}),
		"schema", integer("Configuration schema version. This release reads schema 1.").teamOnly(),
		"forge", str("Where merge requests live: auto (from each remote's host), github, gitlab or none.").
			oneOf("auto", "github", "gitlab", "none").withDefault("auto"),
	),
	"lanes", table("Lanes: the numbered, stable directories agents work in.",
		"max", integer("Most lanes that may exist at once.").between(1, 999).withDefault(int64(10)),
		"pool", integer("Clean lanes kept warm for reuse.").atLeast(0).withDefault(int64(3)),
		"retention", duration("Pooled lanes older than this are destroyed.").withDefault("14d"),
		"hostname", str("Each lane's hostname. {lane} is the lane number, {namespace} the namespace.").
			withDefault("lane-{lane}.localhost").checked(func(v any) (string, string) {
			if !strings.Contains(v.(string), "{lane}") {
				return "does not contain {lane}, so every lane would get the same hostname", `include {lane}, e.g. "lane-{lane}.localhost"`
			}
			return "", ""
		}),
		"server_capacity", integer("Capacity of the implicit resource \"servers\", which holds every server not "+
			"listed in a resource.").atLeast(1).withDefault(int64(2)),
		"port_stride", integer("A server in seat k listens on port + port_stride × k.").atLeast(1).withDefault(int64(100)),
		"branch_prefix", str("Prefix of the branches lanes create: aw/checkout-tax.").withDefault("aw/").
			checked(func(v any) (string, string) {
				s := v.(string)
				if s == "" || strings.ContainsAny(s, " ~^:?*[\\") || strings.Contains(s, "..") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "-") {
					return "is not a valid branch prefix", `use a prefix such as "aw/" or "lane-"`
				}
				return "", ""
			}),
		"data", mapOf("Per-lane data, owned by a provider. The key names the data, e.g. db.", nameRE, nameFix,
			table("",
				"provider", str("postgres, redis, or external (through the extension protocol).").oneOf("postgres", "redis", "external"),
				"command", str("For provider = \"external\": the provider executable."),
				"template", str("postgres: the template database lanes are cloned from. Nothing ever connects to it."),
				"build_template", str("postgres: builds the template when it does not exist, once, however many lanes start together. It runs in "+
					"the owner checkout with AW_TEMPLATE and AW_OWNER set, and may use aw run. Without it, a machine with empty volumes has no template."),
				"databases", strList("postgres: the databases each lane gets, named <database>_<lane>."),
				"url", str("postgres or redis: where the server is, e.g. postgres://postgres@localhost:5432. Keep a password out of "+
					"the team layer: put this key in the individual layer, or use PGPASSWORD or REDISCLI_AUTH."),
				"terminate", boolean("postgres: end other connections to a database before dropping or restoring it.").withDefault(false),
				"service", str("Run the provider's client (psql, redis-cli) in this container instead of on the host: a Compose service "+
					"with one running container, or a container's exact name."),
				"env", mapOf("Variables the lane's commands receive, e.g. DATABASE_URL = \"{url:shop_development}\". "+
					"{lane} and {name} are the lane's number and name.",
					envRE, envFix, str("")),
			)),
	),
	"lifecycle", table("Workspace-level lifecycle hooks.",
		"prepare", hook("After data is provisioned, before installs. Writes each project's env file."),
		"teardown", hook("Before data is dropped, when a lane is destroyed."),
	),
	"projects", mapOf("The project repositories. The key is the project's name.", nameRE, nameFix,
		table("",
			"directory", str("Where the repository lives, relative to the workspace. Defaults to the project's name. "+
				"\".\" makes a single-repository workspace."),
			"repo", str("Clone URL."),
			"lanes", str("worktree: every lane gets a worktree. shared: lanes run the owner checkout with lane settings.").
				oneOf("worktree", "shared").withDefault("worktree"),
			"runner", str("Where commands run: host, or devcontainer (one container per project, shared with VS Code).").
				oneOf("host", "devcontainer").withDefault("host"),
			"install", str("Installs dependencies, in each lane's worktree. Rerun when its stamp changes."),
			"install_stamp", strList("Files, relative to the project, whose contents decide whether install reruns, e.g. package-lock.json."),
			"run", str("The default command for aw run <project> with no command."),
			"needs", strList("Resources the project's commands need."),
			"smoke", str("A quick command that proves the project works in a lane. aw rehearse runs it."),
			"depends_on", strList("Projects this one consumes. A change set merges those first (R69)."),
			"publication", strList("Actions pre-authorized in this project, as in [publication] allow.").checked(oneOfEach(publicationActions...)),
			"generated_contracts", strList("Files generated from a contract, from the project root (** matches directories). Agents may not edit them (R70)."),
			"secrets", strList("Files that are not in Git, such as .env, linked read-only into every lane from the owner "+
				"checkout, relative to the project."),
			"local_files", strList("Gitignored files that are not secrets, such as a local properties file or a large generated "+
				"schema, copied into every new lane from the owner checkout, relative to the project. Only the files named here are "+
				"copied, as a clone where the file system can. Use secrets for anything an agent should not read."),
			"accepted_findings", mapOf("Readiness findings a person has reviewed, by file: the key is a path relative to the project "+
				"(dir/** matches a directory), and the value says why it is fine. aw rehearse and aw doctor --projects skip them.",
				acceptedRE, "name a file relative to the project, e.g. \".env.template\" or \"ci/**\"", str("")),
			"post_checkout", str("run: the repository's own post-checkout hook runs as a setup step of each lane, which can be retried. skip: it does not run in lanes.").
				oneOf("run", "skip").withDefault("run"),
			"env", mapOf("Variables for this project's hooks and aw run commands, such as a switch one of its hooks reads.", envRE, envFix, str("")),
			"generated", strList("Ignored paths (Git pathspecs) that builds create and that do not block recycling a lane, "+
				"such as node_modules or tmp. Other ignored files may be someone's work, so they keep the lane."),
			"lane_guards", mapOf("Checks a branch must pass before a lane is created on it (R28). The key names the guard.",
				nameRE, nameFix, table("",
					"file", str("A file in the branch, relative to the project."),
					"matches", str("A regular expression the file must match."),
					"message", str("What is wrong with a branch that fails, and how to fix it."),
				)),
			"lifecycle", table("Per-project lifecycle hooks.",
				"prepare", hook("After the workspace's prepare, before installs, in the project's worktree."),
				"setup", hook("After installs, for a newly created lane."),
				"reset", hook("Instead of setup, for a lane recycled from the pool."),
				"restore", hook("After a snapshot is restored into the lane. The place to migrate data behind the code."),
			),
			"servers", mapOf("Servers the project runs. The key names the server, e.g. rails.", nameRE, nameFix,
				table("",
					"command", str("Starts the server. {port} is the seat's port. Should exec the server process."),
					"port", integer("Base port, used by the owner's seat 0. Seat k uses port + port_stride × k.").between(1, 65535),
					"owner_port", integer("A process listening here is the owner's copy, started without aw.").between(1, 65535),
					"after", strList("Servers that must start first: <server> in the same project, or <project>.<server>."),
					"ready", str("A command that exits 0 when the server is ready."),
				)),
		)),
	"resources", mapOf("Resources lanes queue for. The key names the resource.", nameRE, nameFix,
		table("",
			"kind", str("process: owns servers. token: hands out a handle.").oneOf("process", "token"),
			"capacity", integer("Seats. For a token resource with tokens, the number of tokens.").atLeast(1),
			"servers", strList("process: the servers, as <project>.<server>, started and stopped together."),
			"tokens", strList("token: the handles, e.g. emulator-5554."),
			"acquire", str("token: extension-protocol command that returns a new token."),
			"release", str("token: extension-protocol command that releases a token."),
			"owner_counts", boolean("The owner's own copy uses a seat.").withDefault(true),
			"owner_preempts", boolean("The owner cuts in line and evicts the longest holder.").withDefault(true),
			"evict_grace", duration("SIGTERM to the process group, then SIGKILL after this.").withDefault("15s"),
			"max_hold", duration("While others wait, a holder is evicted after this. 0 means no limit.").withDefault("0s"),
			"idle_expiry", duration("A lease with no activity for this long is released. 0 means never.").withDefault("0s"),
			"memory", str("Approximate memory per seat, e.g. \"12GB\", for aw doctor's machine check."),
		)),
	"commands", mapOf("Named team commands, run with aw <name>.", nameRE, nameFix,
		table("",
			"project", str("The project the command runs in."),
			"run", str("The command."),
			"needs", strList("Resources it holds while it runs."),
			"after", strList("Lifecycle steps to wait for besides the defaults (the workspace's prepare and this project's steps), "+
				"as <project>.<step>, e.g. api.setup."),
		)),
	"guards", mapOf("The team's own guards (R54), decided in-process on every tool call. The key names the guard. "+
		"To stop people overriding them, lock guards.*.", nameRE, nameFix,
		table("",
			"commands", strList("Command patterns, such as \"terraform apply\". * matches any one word. Options may come between the words, and a word whose value cannot be determined counts as a match."),
			"paths", strList("Files agents may not write, from the workspace root. ** matches any number of directories. A lane's copy of a file counts as that file."),
			"action", str("What happens to a matching call.").oneOf("deny", "ask").withDefault("deny"),
			"message", str("Why it is refused, and what to do instead."),
			"requires", strList("Files that must exist, from the workspace root. While one is missing, everything the guard matches is refused."),
		)),
	"locks", table("Keys the individual layer may not override. Team layer only.",
		"keys", strList("Key patterns. * matches one key segment; a pattern locks everything under it.").teamOnly(),
	),
	"user", table("Personal preferences, usually set in the individual layer.",
		"harness", str("The harness aw new launches: claude, codex, grok, opencode, or another on PATH.").withDefault("claude").
			checked(func(v any) (string, string) {
				if !nameRE.MatchString(v.(string)) {
					return "is not a harness name", "name the harness's command, e.g. \"claude\""
				}
				return "", ""
			}),
		"multiplexer", muxNode("Where aw new, resume and pair open sessions with --mux: none, herdr, tmux, or a command "+
			"template, { command = \"zellij run --name {name} -- {cmd}\" }."),
		"auto_sweep", boolean("Recycle proven-merged lanes in the background.").withDefault(false),
		"owner_sessions", str("normal: sessions in the owner checkout must attach to a lane before writing. pair: "+
			"every session started in the owner checkout is a pair session.").oneOf("normal", "pair").withDefault("normal"),
	),
	"ports", table("Machine-specific port settings.",
		"offset", integer("Added to every port of this workspace, so two workspaces on one machine do not collide.").
			between(0, 60000).withDefault(int64(0)),
	),
	"roles", mapOf("Team roles (R65). A lane takes one with aw new --role. No role can override path confinement, secrets, destructive "+
		"Git or owner-only commands. implementer is built in, and denies publish and merge.", nameRE, nameFix,
		table("",
			"allow", strList("Capabilities this role has beyond the default: publish, merge, containers, servers.").checked(oneOfEach(capabilities...)),
			"deny", strList("Capabilities this role does not have: publish, merge, servers.").checked(oneOfEach(capabilities...)),
			"instructions", str("A file, from the workspace root, whose text a session in this role receives when it starts."),
		)),
	"safeguards", table("Optional rules. None is on by default: aw regulates only its own lanes, seats and state, and leaves the rest to the harness and the forge.",
		"enable", strList("Groups to turn on: git-safety (destructive Git), publication (ask before anything outward-facing), secrets, processes (kill and the like), containers.").checked(oneOfEach(safeguardGroups...)).teamOnly(),
	),
	"publication", table("With the publication safeguard on, what agents may publish without asking (R72).",
		"allow", strList("Pre-authorized actions: push (only the lane's own branch), open-merge-request, merge. Force pushes, deleting "+
			"branches, releases and packages are never pre-authorized.").checked(oneOfEach(publicationActions...)),
	),
	"delegation", table("What the human's aw delegate --scope maintenance covers, beyond aw's own maintenance commands.",
		"maintenance", strList("Commands, as their first word, that a delegated session in the owner checkout may run."),
	),
	"lane_env", mapOf("Variables lanes receive, grouped by whoever adds them, usually a module. {lane}, {namespace} and {port:N} are filled in.", nameRE, nameFix,
		mapOf("", envRE, envFix, str(""))),
	"webhooks", mapOf("Forwarders of a provider's sandbox webhooks. The most recent lane to run aw webhook <provider> receives them.", nameRE, nameFix,
		table("",
			"listen", str("Runs the forwarder. {url} is where it should send events."),
			"server", str("The server that receives them, as <project>.<server>."),
			"path", str("The path on that server.").withDefault("/"),
		)),
	"harnesses", mapOf("Declarative adapters: another hooked harness, without a new release. Each key names a field of its hook payload.", nameRE, nameFix,
		table("",
			"session", str("The field holding the session's ID."),
			"cwd", str("The field holding the working directory."),
			"event", str("The field holding the event's name."),
			"tool", str("The field holding the tool's name."),
			"input", str("The field holding the tool's input, an object."),
			"command", str("The field of the input holding a shell command."),
			"path", str("The field of the input holding a file path."),
			"events", mapOf("The harness's event names, each mapped to pre-tool-use, session-start, user-prompt-submit or session-end.", regexp.MustCompile(`^.+$`), "name the event", str("")),
			"shell_tools", strList("Tools that run a shell command."),
			"read_tools", strList("Tools that read files."),
			"write_tools", strList("Tools that write files."),
			"other_tools", strList("Tools with no path to check."),
		)),
	"plans", table("The plan mechanism.",
		"enabled", boolean("Set false to opt out: aw plan refuses and aw check-docs skips the plan rules.").withDefault(true),
	),
	"devcontainer", table("The devcontainer CLI, for projects whose runner is devcontainer. Pin it: a CLI that drifts breaks builds.",
		"command", str("Runs the CLI. {version} is replaced by version. aw doctor warns when the command names no version.").
			withDefault("npx -y @devcontainers/cli@{version}"),
		"version", str("The CLI version the command uses.").withDefault("0.89.0"),
		"env", mapOf("Variables the CLI runs with. BUILDX_BAKE_ENTITLEMENTS_FS = \"0\" is set unless this overrides it.", envRE, envFix, str("")),
	),
)

var (
	envRE  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	envFix = "use an environment variable name: uppercase letters, digits and underscores"
)

// ParseDuration accepts Go durations ("15s", "1h30m") and days ("14d"). Negative durations are
// refused.
func ParseDuration(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok && d != "" && !strings.ContainsAny(d, "hmsuµn") {
		var n int
		if _, err := fmt.Sscanf(d, "%d", &n); err != nil || fmt.Sprint(n) != d || n < 0 {
			return 0, fmt.Errorf("%q is not a duration", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil || dur < 0 {
		return 0, fmt.Errorf("%q is not a duration", s)
	}
	return dur, nil
}

// validateLayer checks one layer's values against the schema: unknown keys, types, enums, bounds,
// key names, and keys only the team layer may set.
func validateLayer(d *doc, layer string) Errors {
	var errs Errors
	walk(d, layer, d.values, schema, nil, &errs)
	return errs
}

func walk(d *doc, layer string, v any, n *node, path Path, errs *Errors) {
	fail := func(p Path, msg, fix string) {
		*errs = append(*errs, &Error{Pos: d.at(p), Key: p.String(), Msg: msg, Fix: fix})
	}
	if n.team && layer != LayerTeam {
		fail(path, "can only be set in the team layer (workspace.toml)", "remove it from this file")
		return
	}
	switch n.kind {
	case kTable:
		m, ok := v.(map[string]any)
		if !ok {
			fail(path, "must be a table, not "+typeName(v), fmt.Sprintf("write it as [%s] with keys under it", path))
			return
		}
		for _, k := range sortedKeys(m) {
			child, ok := n.fields[k]
			if !ok {
				fail(path.child(k), "unknown key", unknownFix(k, n))
				continue
			}
			walk(d, layer, m[k], child, path.child(k), errs)
		}
	case kMap:
		m, ok := v.(map[string]any)
		if !ok {
			fail(path, "must be a table, not "+typeName(v), fmt.Sprintf("write it as [%s.<name>] tables", path))
			return
		}
		for _, k := range sortedKeys(m) {
			if n.keyRE != nil && !n.keyRE.MatchString(k) {
				fail(path.child(k), fmt.Sprintf("%q is not a valid name", k), n.keyFix)
				continue
			}
			if _, isString := m[k].(string); n.elem.kind == kString && !isString {
				fail(path.child(k), "must be a string, not "+typeName(m[k]),
					fmt.Sprintf("if %s belongs to the table above, move it before [%s]: a TOML table takes every key after it", k, path))
				continue
			}
			walk(d, layer, m[k], n.elem, path.child(k), errs)
		}
	case kMux:
		switch m := v.(type) {
		case string:
			if m != "none" && m != "herdr" && m != "tmux" {
				fail(path, fmt.Sprintf("%q is not a built-in multiplexer", m), `use "none", "herdr", "tmux", or { command = "… {name} -- {cmd}" }`)
			}
		case map[string]any:
			walk(d, layer, m, muxTable, path, errs)
			if c, _ := m["command"].(string); !strings.Contains(c, "{cmd}") {
				fail(path.child("command"), "has no {cmd}, so it would not start the session", `write it like "zellij run --name {name} -- {cmd}"`)
			}
		default:
			fail(path, "must be a name or a table { command = \"…\" }, not "+typeName(v), `write it as "tmux" or { command = "… {cmd}" }`)
		}
	case kHook:
		switch h := v.(type) {
		case string:
			if strings.TrimSpace(h) == "" {
				fail(path, "is empty", "give the command to run, or remove the key")
			}
		case map[string]any:
			walk(d, layer, h, hookTable, path, errs)
			if _, ok := h["run"]; !ok {
				fail(path, "has no run key", `add run = "<command>"`)
			}
		default:
			fail(path, "must be a command string or a table { run = \"…\" }, not "+typeName(v), `write it as "<command>" or { run = "<command>", timeout = "10m" }`)
		}
	default:
		if msg, fix := checkLeaf(v, n); msg != "" {
			fail(path, msg, fix)
		}
	}
}

var muxTable = table("",
	"command", str("Opens a window or pane. {name} is the session's name and {cmd} the command to run in it."),
)

var hookTable = table("",
	"run", str("The command."),
	"timeout", duration("Fails the step after this long."),
	"needs", strList("Resources the step holds while it runs."),
)

func checkLeaf(v any, n *node) (string, string) {
	switch n.kind {
	case kString, kDuration:
		s, ok := v.(string)
		if !ok {
			return "must be a string, not " + typeName(v), fmt.Sprintf("quote it: %q", fmt.Sprint(v))
		}
		if n.kind == kDuration {
			if _, err := ParseDuration(s); err != nil {
				return "is not a duration", `write a duration such as "30s", "15m", "2h" or "14d"`
			}
		}
		if len(n.enum) > 0 && !slices.Contains(n.enum, s) {
			return fmt.Sprintf("must be one of %s, not %q", strings.Join(n.enum, ", "), s), "use " + quoteAll(n.enum)
		}
	case kInt:
		i, ok := v.(int64)
		if !ok {
			return "must be an integer, not " + typeName(v), "write a whole number without quotes"
		}
		if n.min != nil && i < int64(*n.min) {
			return fmt.Sprintf("must be at least %d", *n.min), fmt.Sprintf("use a number of %d or more", *n.min)
		}
		if n.max != nil && i > int64(*n.max) {
			return fmt.Sprintf("must be at most %d", *n.max), fmt.Sprintf("use a number of %d or less", *n.max)
		}
	case kBool:
		if _, ok := v.(bool); !ok {
			return "must be true or false, not " + typeName(v), "write true or false without quotes"
		}
	case kStringList:
		arr, ok := v.([]any)
		if !ok {
			return "must be an array of strings, not " + typeName(v), fmt.Sprintf("write it as [%s]", tomlValue(fmt.Sprint(v)))
		}
		for _, e := range arr {
			if _, ok := e.(string); !ok {
				return "must contain only strings, not " + typeName(e), "quote every element"
			}
		}
	}
	if n.check != nil {
		return n.check(v)
	}
	return "", ""
}

func unknownFix(k string, n *node) string {
	known := slices.Clone(n.order)
	best, bestDist := "", 3
	for _, name := range known {
		if d := editDistance(k, name); d < bestDist {
			best, bestDist = name, d
		}
	}
	if best != "" {
		return fmt.Sprintf("did you mean %s? Known keys here: %s", best, strings.Join(known, ", "))
	}
	return "remove it. Known keys here: " + strings.Join(known, ", ")
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case int64:
		return "an integer"
	case float64:
		return "a number with a fraction"
	case bool:
		return "a boolean"
	case []any:
		return "an array"
	case map[string]any:
		return "a table"
	}
	return "a date or time"
}

func quoteAll(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(q, ", ")
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// defaults builds the default layer from the schema.
func defaults() map[string]any {
	var build func(n *node) map[string]any
	build = func(n *node) map[string]any {
		out := map[string]any{}
		for _, k := range n.order {
			f := n.fields[k]
			switch {
			case f.kind == kTable:
				if sub := build(f); len(sub) > 0 {
					out[k] = sub
				}
			case f.def != nil:
				out[k] = f.def
			}
		}
		return out
	}
	return build(schema)
}

// elemDefaults are defaults that apply inside every entry of a map, such as a project's runner.
// They are filled in when the typed configuration is built, and shown as "default" provenance.
func elemDefaults(n *node) map[string]any {
	out := map[string]any{}
	for _, k := range n.order {
		if f := n.fields[k]; f.def != nil && f.kind != kTable {
			out[k] = f.def
		}
	}
	return out
}

// lookupSchema returns the schema node for a path, or nil.
func lookupSchema(p Path) *node {
	n := schema
	for _, seg := range p {
		switch n.kind {
		case kTable:
			n = n.fields[seg]
		case kMap:
			n = n.elem
		case kHook:
			n = hookTable.fields[seg]
		case kMux:
			n = muxTable.fields[seg]
		default:
			return nil
		}
		if n == nil {
			return nil
		}
	}
	return n
}
