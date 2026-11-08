package lanes

import "testing"

func TestName(t *testing.T) {
	cases := map[string]string{
		"fix checkout tax rounding":        "checkout-tax",
		"add feature flag":                 "feature-flag",
		"Upgrade to Rails 8.1":             "rails-8",
		"Onboarding flow v2":               "onboarding-flow",
		"add webhook retries":              "webhook-retries",
		"the":                              "lane",
		"":                                 "lane",
		"🚀 launch":                         "launch",
		"2fa for admins":                   "lane-2fa-admins",
		"fix":                              "fix",
		"make the checkout-page faster!!!": "checkout-page",
		"Refactor   AuthenticationService": "authenticationservice",
	}
	for in, want := range cases {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
		if !ValidName(Name(in)) {
			t.Errorf("Name(%q) = %q is not valid", in, Name(in))
		}
	}
}

func TestUnique(t *testing.T) {
	taken := map[string]bool{"checkout-tax": true, "checkout-tax-2": true}
	if got := Unique("checkout-tax", func(s string) bool { return taken[s] }); got != "checkout-tax-3" {
		t.Errorf("got %q", got)
	}
	long := "a234567890123456789012345678901b"
	taken = map[string]bool{long: true}
	if got := Unique(long, func(s string) bool { return taken[s] }); len(got) > MaxNameLength || !ValidName(got) {
		t.Errorf("got %q", got)
	}
}

func TestCheckName(t *testing.T) {
	for _, bad := range []string{"", "../x", "Checkout", "a b", "x;rm", "-x", "x-", "a--b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if CheckName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"checkout-tax", "lane-2fa", "x"} {
		if CheckName(ok) != nil {
			t.Errorf("%q refused", ok)
		}
	}
}
