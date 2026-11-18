package policy

import "testing"

// TestSuggestionsPass checks decision 7 of plans/dogfood-0.4.md: every command aw tells an agent to
// run passes the guard as the session it is shown to, without a prompt. A hook's ask overrides
// the user's own allow rules, so a hint that leads to a prompt is a dead end. Each row names the
// message the command comes from; a new hint addressed to an agent gets a row here.
func TestSuggestionsPass(t *testing.T) {
	f := newFixture(t)
	lane := []tcase{
		{"bin/aw setup checkout-tax --project api", Allow, ""}, // policy/rules.go: a project not set up yet
		{"bin/aw setup checkout-tax", Allow, ""},               // lifecycle: a failed step
		{"bin/aw setup 01", Allow, ""},
		{"bin/aw setup 1 --project=web --json", Allow, ""},
		{"bin/aw run api npm test", Allow, ""},     // policy/rules.go, compose
		{"bin/aw serve web", Allow, ""},            // broker: queue again, take a seat again
		{"bin/aw serve web --wait", Allow, ""},     // serve.go
		{"bin/aw stop web", Allow, ""},             // broker
		{"bin/aw push", Allow, ""},                 // push.go
		{"bin/aw push --as feature-x", Allow, ""},  // githook, policy/rules.go
		{"bin/aw reset-branch", Allow, ""},         // reset.go
		{"bin/aw finish", Allow, ""},               // compose, reset.go
		{"bin/aw detach", Allow, ""},               // compose
		{"bin/aw whoami", Allow, ""},               // hook, resume
		{"bin/aw lanes", Allow, ""},                // compose
		{"bin/aw note --done 1", Allow, ""},        // hook
		{"bin/aw doctor", Allow, ""},               // recovery, launcher
		{"bin/aw config validate", Allow, ""},      // recovery
		{"bin/aw changelog --json", Allow, ""},     // audit
		{"bin/aw inspect --json", Allow, ""},       // onboarding
		{"bin/aw rehearse", Allow, ""},             // onboarding, sandbox
		{"bin/aw containers claim web", Allow, ""}, // doctor
		{"bin/aw delegate --status", Allow, ""},    // hook
		{"bin/aw sweep", Allow, ""},                // the sweep hints without --apply
		{"bin/aw archives", Allow, ""},             // listing, without --restore
	}
	s := f.scope(Lane)
	s.Safeguards = []string{"git-safety", "secrets", "processes", "containers"}
	run(t, s, f.lane, lane)
	// With the opt-in publication safeguard, pushing is the guard tier on purpose: it is
	// outward-facing, so the human approves it.
	run(t, f.scope(Lane), f.lane, []tcase{{"bin/aw push", Deny, "publication"}})
	unattached := []tcase{
		{"bin/aw attach 'fix the tax rounding'", Allow, ""}, // policy.go: the first write
		{"bin/aw whoami", Allow, ""},
		{"bin/aw doctor", Allow, ""},
		{"bin/aw lanes", Allow, ""},
		{"bin/aw sweep", Allow, ""},
	}
	run(t, f.scope(Unattached), f.owner, unattached)
}
