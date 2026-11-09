#!/bin/sh
# The setup/fresh-clone check against a published release (plan M7): install a global aw, make a
# workspace pinned to the release, then clone it with an empty cache and run bin/aw setup, which must
# download the pinned binary and verify its checksum and signature. Needs sh, git, curl or wget,
# sha256sum or shasum, and cosign.
# Usage: fresh-clone.sh <version>
set -eu
version=$1
script_dir=$(cd "$(dirname "$0")" && pwd -P)
command -v cosign >/dev/null || { echo "cosign is required for this check" >&2; exit 1; }

work=$(mktemp -d)
export HOME="$work/home" XDG_CACHE_HOME="$work/cache" XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data" XDG_STATE_HOME="$work/state"
export AW_INSTALL_DIR="$work/bin" PATH="$work/bin:$PATH" GIT_CONFIG_NOSYSTEM=1
mkdir -p "$HOME"
git config --global user.name fresh-clone
git config --global user.email fresh-clone@example.invalid
git config --global init.defaultBranch main

AW_VERSION=$version sh "$script_dir/../../install.sh"
aw version | grep -F "aw $version "

git init -q --bare "$work/remote.git"
git clone -q "$work/remote.git" "$work/maintainer" 2>/dev/null
cd "$work/maintainer"
echo '# fresh' > README.md
git add -A && git commit -q -m first
aw init
git add -A && git commit -q -m "Adopt aw $version"
git push -q origin main

rm -rf "$XDG_CACHE_HOME"
git clone -q "$work/remote.git" "$work/member"
cd "$work/member"
bin/aw setup --harness claude >"$work/setup.log" 2>&1 || {
	cat "$work/setup.log"
	echo "bin/aw setup failed" >&2
	exit 1
}
cat "$work/setup.log"
if grep -q 'has no signature' "$work/setup.log"; then
	echo "the release's signature was not verified" >&2
	exit 1
fi
test -x "$XDG_CACHE_HOME/agent-workspace/bin/$version/aw"
bin/aw doctor
bin/aw attach 'fresh clone check'
bin/aw setup --harness claude | grep -q 'already set up'
echo "fresh clone: passed"
