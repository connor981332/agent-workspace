#!/bin/sh
# Checks that every action a workflow uses exists at the tag or branch it names. A wrong one fails
# only when the workflow runs, which for the release workflow means after a tag has been pushed.
# Needs gh. Run it before tagging.
set -eu
cd "$(dirname "$0")/../.."
status=0
for use in $(sed -n 's/^[[:space:]-]*uses:[[:space:]]*\([^.[:space:]][^[:space:]]*@[^[:space:]]*\).*/\1/p' .github/workflows/*.yml | sort -u); do
	action=${use%@*}
	ref=${use##*@}
	repo=$(echo "$action" | cut -d/ -f1,2)
	if gh api "repos/$repo/git/ref/tags/$ref" >/dev/null 2>&1 || gh api "repos/$repo/git/ref/heads/$ref" >/dev/null 2>&1; then
		echo "ok   $use"
	else
		echo "MISSING  $use" >&2
		status=1
	fi
done
exit $status
