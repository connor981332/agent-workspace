#!/bin/sh
# bin/aw: the Agent Workspace launcher. Managed by aw (do not edit: aw init and aw setup rewrite it).
#
# It runs the aw version pinned in the owner checkout's .aw/version, verified against .aw/lock.
# On the common path it uses only shell builtins and then execs the cached binary. Only when the
# binary is not cached does it download it (from $AW_MIRROR if set), verify its SHA-256 checksum
# and store it. AW_TOOL_PATH runs a local build instead, for working on aw itself.
#
# For aw hook and aw git-hook, every failure exits 2, which harnesses and Git treat as a refusal:
# a guard that cannot run must deny, never allow.

case ${1:-} in
hook | git-hook) aw_hook=1 ;;
*) aw_hook= ;;
esac

aw_die() {
	echo "aw: $*" >&2
	if [ -n "$aw_hook" ]; then exit 2; fi
	exit 1
}

# Run the binary. For hooks it runs as a child, so that a binary that fails to start still exits 2.
aw_run() {
	if [ -z "$aw_hook" ]; then
		exec "$@"
	fi
	"$@"
	aw_status=$?
	case $aw_status in
	0 | 2) exit $aw_status ;;
	esac
	echo "aw: $1 exited with status $aw_status, so this call is refused → run bin/aw doctor" >&2
	exit 2
}

if [ -n "${AW_TOOL_PATH:-}" ]; then
	aw_run "$AW_TOOL_PATH" "$@"
fi

case $0 in
*/bin/aw) aw_root=${0%/bin/aw} ;;
bin/aw) aw_root=. ;;
*) aw_die "cannot find the workspace from $0 → run the launcher as <workspace>/bin/aw" ;;
esac
[ -n "$aw_root" ] || aw_root=/

# 1. The owner checkout: a lane root points to it; otherwise this is it.
aw_owner=$aw_root
if [ -f "$aw_root/.aw/owner" ]; then
	IFS= read -r aw_owner <"$aw_root/.aw/owner" || [ -n "$aw_owner" ] ||
		aw_die "$aw_root/.aw/owner is empty → recreate the lane"
fi

# 2. The pin: an exact version, such as 1.4.2 or 1.5.0-rc.1. Ranges, branches and tags such as
# "latest" are refused, because a pin must name exactly one build.
aw_version=
IFS= read -r aw_version <"$aw_owner/.aw/version" || [ -n "$aw_version" ] ||
	aw_die "$aw_owner/.aw/version is missing or empty → restore it from Git, or run aw init"
aw_core=${aw_version%%[-+]*}
aw_major=${aw_core%%.*}
aw_rest=${aw_core#*.}
aw_minor=${aw_rest%%.*}
aw_patch=${aw_rest#*.}
for aw_part in "$aw_major" "$aw_minor" "$aw_patch"; do
	case $aw_part in
	'' | *[!0-9]*) aw_die "$aw_owner/.aw/version is \"$aw_version\", which is not an exact version → pin an exact release such as 1.4.2" ;;
	esac
done
case $aw_core in
*.*.*) ;;
*) aw_die "$aw_owner/.aw/version is \"$aw_version\", which is not an exact version → pin an exact release such as 1.4.2" ;;
esac
case $aw_version in
*[!0-9A-Za-z.+-]*) aw_die "$aw_owner/.aw/version is \"$aw_version\", which is not an exact version → pin an exact release such as 1.4.2" ;;
esac

# 3. The cached binary.
aw_cache=${XDG_CACHE_HOME:-${HOME:-}/.cache}/agent-workspace/bin/$aw_version
if [ -x "$aw_cache/aw" ]; then
	aw_run "$aw_cache/aw" "$@"
fi

# 4. Not cached: download, verify, store atomically. Here, and only here, external commands run.
case $(uname -s) in
Linux) aw_os=linux ;;
Darwin) aw_os=darwin ;;
*) aw_die "aw supports Linux and macOS only, not $(uname -s)" ;;
esac
case $(uname -m) in
x86_64 | amd64) aw_arch=amd64 ;;
aarch64 | arm64) aw_arch=arm64 ;;
*) aw_die "aw supports amd64 and arm64 only, not $(uname -m)" ;;
esac
aw_asset=aw_${aw_version}_${aw_os}_${aw_arch}

aw_sum=
while read -r aw_line_sum aw_line_file || [ -n "$aw_line_sum" ]; do
	if [ "$aw_line_file" = "$aw_asset" ]; then aw_sum=$aw_line_sum; fi
done <"$aw_owner/.aw/lock" 2>/dev/null
case $aw_sum in
'') aw_die "$aw_owner/.aw/lock has no checksum for $aw_asset → restore .aw/lock from Git, or re-pin with the release's checksums.txt" ;;
*[!0-9a-f]*) aw_die "$aw_owner/.aw/lock has an invalid checksum for $aw_asset" ;;
esac
[ ${#aw_sum} -eq 64 ] || aw_die "$aw_owner/.aw/lock has an invalid checksum for $aw_asset"

mkdir -p "$aw_cache" || aw_die "cannot create $aw_cache"
aw_tmp=$aw_cache/.download.$$
trap 'rm -f "$aw_tmp"' EXIT
aw_from=${AW_MIRROR:-https://github.com/connor981332/agent-workspace/releases/download/v$aw_version}
case $aw_from in
/*) cp "$aw_from/$aw_asset" "$aw_tmp" 2>/dev/null || aw_die "$aw_from/$aw_asset not found in the mirror (AW_MIRROR)" ;;
*)
	echo "aw: downloading $aw_asset" >&2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$aw_tmp" "$aw_from/$aw_asset" || aw_die "downloading $aw_from/$aw_asset failed → check the network, or set AW_MIRROR"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$aw_tmp" "$aw_from/$aw_asset" || aw_die "downloading $aw_from/$aw_asset failed → check the network, or set AW_MIRROR"
	else
		aw_die "neither curl nor wget is installed → install one, then retry"
	fi
	;;
esac
if command -v sha256sum >/dev/null 2>&1; then
	aw_got=$(sha256sum "$aw_tmp")
elif command -v shasum >/dev/null 2>&1; then
	aw_got=$(shasum -a 256 "$aw_tmp")
else
	aw_die "neither sha256sum nor shasum is installed, so the download cannot be verified → install one, then retry"
fi
aw_got=${aw_got%% *}
[ "$aw_got" = "$aw_sum" ] || aw_die "checksum mismatch for $aw_asset (expected $aw_sum, got $aw_got); it was not installed → check AW_MIRROR, or report this"
chmod 755 "$aw_tmp" && mv -f "$aw_tmp" "$aw_cache/aw" || aw_die "cannot store $aw_cache/aw"
trap - EXIT
aw_run "$aw_cache/aw" "$@"
