#!/bin/sh
# Installs a global aw to ~/.local/bin, verifying its checksum, and its signature when cosign is
# installed. Inside a workspace the global aw runs that workspace's pinned version, so this only
# needs to be good enough to run aw init.
#
#   curl -fsSL https://raw.githubusercontent.com/connor981332/agent-workspace/master/install.sh | sh
#
# AW_VERSION picks a release (default: the newest, pre-releases included), AW_INSTALL_DIR the
# directory, and AW_MIRROR a release mirror: a directory, or a URL.
set -eu

repo=connor981332/agent-workspace
die() {
	echo "install.sh: $*" >&2
	exit 1
}

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "aw supports Linux and macOS only, not $(uname -s)" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "aw supports amd64 and arm64 only, not $(uname -m)" ;;
esac

# get <url> <path>
get() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 4 --retry-delay 2 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1" || { sleep 2; wget -q -O "$2" "$1"; } || { sleep 4; wget -q -O "$2" "$1"; }
	else
		die "neither curl nor wget is installed → install one, then retry"
	fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

version=${AW_VERSION:-}
if [ -z "$version" ]; then
	get "https://api.github.com/repos/$repo/releases?per_page=1" "$tmp/releases" ||
		die "could not ask GitHub for the newest release → set AW_VERSION"
	version=$(sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p' "$tmp/releases" | head -n 1)
	[ -n "$version" ] || die "GitHub lists no release → set AW_VERSION"
fi
version=${version#v}
case $version in
[0-9]*.[0-9]*.[0-9]* | [0-9]*.[0-9]*.[0-9]*[-+]*) ;;
*) die "\"$version\" is not an exact version" ;;
esac

from=${AW_MIRROR:-https://github.com/$repo/releases/download/v$version}
fetch() {
	case $from in
	/*) cp "$from/$1" "$2" 2>/dev/null ;;
	*) get "$from/$1" "$2" ;;
	esac
}

# Whether the release really has no such file, as opposed to the server failing to give it.
absent() {
	case $from in
	/*) [ ! -e "$from/$1" ] ;;
	*)
		if command -v curl >/dev/null 2>&1; then
			[ "$(curl -sSL -o /dev/null -w '%{http_code}' "$from/$1")" = 404 ]
		else
			wget -S -O /dev/null "$from/$1" 2>&1 | grep -q ' 404'
		fi
		;;
	esac
}

asset=aw_${version}_${os}_${arch}
echo "Downloading $asset"
fetch "$asset" "$tmp/aw" || die "could not get $asset from $from → check the version and the network"
fetch checksums.txt "$tmp/checksums.txt" || die "could not get checksums.txt from $from"

want=$(while read -r sum file; do if [ "$file" = "$asset" ]; then echo "$sum"; fi; done <"$tmp/checksums.txt")
[ -n "$want" ] || die "checksums.txt does not list $asset"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/aw")
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "$tmp/aw")
else
	die "neither sha256sum nor shasum is installed, so the download cannot be verified"
fi
[ "${got%% *}" = "$want" ] || die "checksum mismatch for $asset (expected $want, got ${got%% *}); nothing was installed"

if command -v cosign >/dev/null 2>&1; then
	if fetch checksums.txt.sigstore.json "$tmp/bundle"; then
		cosign verify-blob --bundle "$tmp/bundle" \
			--certificate-oidc-issuer https://token.actions.githubusercontent.com \
			--certificate-identity-regexp '^https://github\.com/connor981332/agent-workspace/\.github/workflows/release\.yml@refs/tags/v' \
			"$tmp/checksums.txt" >/dev/null 2>&1 ||
			die "the signature of the checksums did not verify; nothing was installed → report this"
		echo "Verified the release's signature"
	elif absent checksums.txt.sigstore.json; then
		echo "This release has no signature to verify; its checksum matched"
	else
		die "could not fetch the release's signature from $from; nothing was installed → check the network, and retry"
	fi
fi

dir=${AW_INSTALL_DIR:-${HOME:?}/.local/bin}
mkdir -p "$dir"
chmod 755 "$tmp/aw"
mv -f "$tmp/aw" "$dir/aw"
echo "Installed aw $version in $dir"
case :$PATH: in
*:"$dir":*) ;;
*) echo "Add $dir to your PATH." ;;
esac
