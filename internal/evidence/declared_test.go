package evidence

import "testing"

// TestDeclared pins the glob rules generated and accepted_findings share, including the cases from
// the 0.4.0 dogfood report, where ** acted like * unless it came last.
func TestDeclared(t *testing.T) {
	for _, tc := range []struct {
		pattern, entry string
		want           bool
	}{
		{"**", "README.md", true},
		{"**", "deploy/script.sh", true},
		{"**", "a/b/c/d/e.sh", true},
		{"**/*", "README.md", true},
		{"**/*", "deploy/script.sh", true},
		{"**/*", "a/b/c/d/e.sh", true},
		{"deploy/**/*", "deploy/script.sh", true},
		{"deploy/**/*", "deploy/sub/script.sh", true},
		{"deploy/**/*", "other/script.sh", false},
		{"dir/*/*/*", "dir/a/b/c", true},
		{"dir/*/*/*", "dir/a/b/c/d", true}, // dir/a/b/c is a directory that holds it
		{"dir/*/*/*", "dir/a/b", false},
		{"cypress/**", "cypress/a/b/c/d.js", true},
		{"subdir", "subdir/a/b.txt", true},
		{"subdir/", "subdir/a/b.txt", true},
		{"subdir", "other/subdir/b.txt", true}, // no slash: a name at any depth, as in .gitignore
		{"sub/dir", "other/sub/dir/b.txt", false},
		{"**/build/**", "a/b/build/out.o", true},
		{"**/build/**", "build/out.o", true},
		{"**/rspec.xml", "spec/reports/rspec.xml", true},
		{"*.log", "log/development.log", true},
		{"log/*.log", "log/development.log", true},
		{"log/*.log", "other/log/development.log", false},
		{"db/schema.rb", "db/schema.rb", true},
		{"db/schema.rb", "db/schema.rb.bak", false},
		{"tmp/", "tmp/", true},
		{"/.env", ".env", true},
		{"/.env", "config/.env", false}, // a leading slash anchors it, as in .gitignore
		{".env", "config/.env", true},
	} {
		if got := Declared(tc.entry, []string{tc.pattern}); got != tc.want {
			t.Errorf("Declared(%q, %q) = %v, want %v", tc.entry, tc.pattern, got, tc.want)
		}
	}
}
