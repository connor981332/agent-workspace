package config

import (
	"os"
	"strings"
	"testing"
)

const optionsTeam = `[workspace]
name = "Shop"
namespace = "shop"
schema = 1

[projects.monolith]
directory = "monolith"

[projects.monolith.env]
CONSUMER_GROUP = "shared"

[options.event-bus]
default = "in-memory"
description = "Run the event bus in memory, or on the project's real Kafka"

[options.event-bus.choices.in-memory]
projects.monolith.env = { EVENT_BUS_DEV_MODE = "true" }

[options.event-bus.choices.real]
projects.monolith.env = { EVENT_BUS_DEV_MODE = "false", CONSUMER_GROUP = "lane-{lane}" }

[options.runner]
default = "host"

[options.runner.choices.host]

[options.runner.choices.devcontainer]
projects.monolith.runner = "devcontainer"
`

func loadWith(t *testing.T, team, indiv string, override map[string]string) (*Config, error) {
	t.Helper()
	return loadChoosing("/ws/workspace.toml", []byte(team), func(string) (string, []byte, error) {
		if indiv == "" {
			return "/home/u/.config/agent-workspace/shop.toml", nil, os.ErrNotExist
		}
		return "/home/u/.config/agent-workspace/shop.toml", []byte(indiv), nil
	}, override)
}

func TestOptionsDefaultsAndChoices(t *testing.T) {
	c, err := loadWith(t, optionsTeam, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := c.Projects["monolith"].Env
	if env["EVENT_BUS_DEV_MODE"] != "true" || env["CONSUMER_GROUP"] != "shared" || c.Projects["monolith"].Runner != "host" {
		t.Errorf("defaults: env %v, runner %q", env, c.Projects["monolith"].Runner)
	}
	if c.Chosen["event-bus"] != "in-memory" || c.Chosen["runner"] != "host" {
		t.Errorf("chosen: %v", c.Chosen)
	}

	c, err = loadWith(t, optionsTeam, "[choose]\nevent-bus = \"real\"\n\n[projects.monolith.env]\nCONSUMER_GROUP = \"mine\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	env = c.Projects["monolith"].Env
	// The overlay merges key by key, and the individual layer still comes last.
	if env["EVENT_BUS_DEV_MODE"] != "false" || env["CONSUMER_GROUP"] != "mine" || c.Chosen["event-bus"] != "real" {
		t.Errorf("a pick: env %v, chosen %v", env, c.Chosen)
	}
	prov, _ := c.Explain(Path{"projects", "monolith", "env", "EVENT_BUS_DEV_MODE"})
	if len(prov) != 1 || !strings.Contains(prov[0].Settings[len(prov[0].Settings)-1].Layer, "option event-bus=real") ||
		prov[0].Settings[len(prov[0].Settings)-1].Pos.Line == 0 {
		t.Errorf("provenance of an overlay: %+v", prov)
	}

	c, err = loadWith(t, optionsTeam, "", map[string]string{"runner": "devcontainer"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Projects["monolith"].Runner != "devcontainer" {
		t.Errorf("an override: runner %q", c.Projects["monolith"].Runner)
	}
}

func TestOptionsRefusals(t *testing.T) {
	for _, tc := range []struct {
		team, indiv, want string
	}{
		{optionsTeam, "[choose]\nevent-bus = \"kafka\"\n", `"kafka" is not a choice of option event-bus`},
		{optionsTeam, "[choose]\nqueue = \"x\"\n", "is not an option of this workspace"},
		{optionsTeam + "\n[choose]\nrunner = \"host\"\n", "", "belongs in the individual file"},
		{strings.Replace(optionsTeam, `default = "host"`, `default = "docker"`, 1), "", `"docker" is not one of its choices`},
		{optionsTeam + "\n[options.other]\ndefault = \"a\"\n[options.other.choices.a]\nprojects.monolith.runner = \"host\"\n", "", "projects.monolith.runner, which option"},
		{optionsTeam + "\n[options.locked]\ndefault = \"a\"\n[options.locked.choices.a]\nlocks.keys = [\"x\"]\n", "", "cannot be set by an option's choice"},
		{optionsTeam + "\n[options.ns]\ndefault = \"a\"\n[options.ns.choices.a]\nsafeguards.enable = [\"secrets\"]\n", "", "is the team's alone"},
		{optionsTeam + "\n[options.bad]\ndefault = \"a\"\n[options.bad.choices.a]\nprojects.monolith.nope = 1\n", "", "unknown key"},
	} {
		_, err := loadWith(t, tc.team, tc.indiv, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want %q, got %v", tc.want, err)
		}
	}
}

func TestOptionsLock(t *testing.T) {
	team := optionsTeam + "\n[locks]\nkeys = [\"choose.runner\"]\n"
	c, err := loadWith(t, team, "[choose]\nrunner = \"devcontainer\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Chosen["runner"] != "host" || c.Projects["monolith"].Runner != "host" {
		t.Errorf("a locked option was chosen: %v", c.Chosen)
	}
	if len(c.Refused) != 1 || c.Refused[0].Key != "choose.runner" {
		t.Errorf("the refusal: %+v", c.Refused)
	}
}
