#!/bin/sh
# Requires the "Hot path" section of the PR description to be filled in when the PR changes a
# hot-path package (plans/release-candidate.md §4.3 rule 3 and §5.4; speed.md: "A pull request
# that adds work to the hot path must say why in its description").
#
# Usage: check-hot-path.sh <base-sha> <head-sha>, with the PR description in $PR_BODY.
set -eu

base=$1
head=$2

# Keep in step with hotPath and hotPathExcluded in internal/archtest/deps_test.go.
hot='^(cmd/aw|internal/(harness|policy|shell|config|state|sessions|workspace|gitx))/'
excluded='^internal/(harness/launch|gitx/run)/'

changed=$(git diff --name-only "$base...$head" -- | grep -E "$hot" | grep -Ev "$excluded" || true)
if [ -z "$changed" ]; then
	echo "No hot-path files changed."
	exit 0
fi

echo "Hot-path files changed:"
printf '%s\n' "$changed" | sed 's/^/  /'

# The text of the "## Hot path" section, without HTML comments and blank lines.
section=$(printf '%s\n' "${PR_BODY:-}" | tr -d '\r' |
	awk '/^##[[:space:]]+Hot path[[:space:]]*$/ { on = 1; next } on && /^##[[:space:]]/ { on = 0 } on' |
	perl -0pe 's/<!--.*?-->//gs' | grep -v '^[[:space:]]*$' || true)

if [ -z "$section" ]; then
	echo "::error title=Hot path section is empty::This PR changes hot-path packages. Fill in the" \
		"\"Hot path\" section of the PR description: what work it adds to a guard decision, and why."
	exit 1
fi
echo "Hot path section:"
printf '%s\n' "$section"
