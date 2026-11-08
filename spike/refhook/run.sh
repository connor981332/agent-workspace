#!/bin/sh
# Ref-hook spike (plans/release-candidate.md M0): record which Git commands fire the
# reference-transaction hook, with which states and refs, from the owner checkout and from a lane
# worktree. Prints TSV: git-version, where, command, state, ref, old, new (zero|oid), and the hook's
# own working directory and GIT_DIR (lane, owner, common-gitdir, lane-gitdir, unset).
#   GIT=/usr/bin/git sh run.sh > results.tsv
set -eu
GIT=${GIT:-git}
ver=$($GIT --version | sed 's/^git version //')
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
log=$tmp/log
export HOME=$tmp/home GIT_CONFIG_NOSYSTEM=1 GIT_AUTHOR_NAME=s GIT_AUTHOR_EMAIL=s@example.invalid \
	GIT_COMMITTER_NAME=s GIT_COMMITTER_EMAIL=s@example.invalid
mkdir -p "$HOME" "$tmp/hooks"
cat > "$tmp/hooks/reference-transaction" <<HOOK
#!/bin/sh
while read -r old new ref; do
	case \$old in 0000000000000000000000000000000000000000) o=zero ;; *) o=oid ;; esac
	case \$new in 0000000000000000000000000000000000000000) n=zero ;; *) n=oid ;; esac
	gd=\${GIT_DIR:-unset}
	case \$gd in */worktrees/*) gd=lane-gitdir ;; *.git|.git) gd=common-gitdir ;; unset) ;; *) gd=other ;; esac
	case \$PWD in */.worktrees/*) cwd=lane ;; */owner) cwd=owner ;; *) cwd=other ;; esac
	printf '%s\t%s\t%s\t%s\t%s\t%s\n' "\$1" "\$ref" "\$o" "\$n" "\$cwd" "\$gd" >> "$log"
done
HOOK
chmod +x "$tmp/hooks/reference-transaction"

g() { $GIT "$@" >/dev/null 2>&1; }
$GIT init -q --bare "$tmp/remote.git" 2>/dev/null || $GIT init -q --bare "$tmp/remote.git"
$GIT init -q "$tmp/owner"
cd "$tmp/owner"
g symbolic-ref HEAD refs/heads/main
g config core.hooksPath "$tmp/hooks"
echo a > a && g add a && g commit -q -m a
g remote add origin "$tmp/remote.git" && g push -q -u origin main
g worktree add -q -b lane-03 "$tmp/owner/.worktrees/03"

# record <where> <description> <command…>: run the command in the owner checkout or the lane.
record() {
	where=$1 desc=$2; shift 2
	: > "$log"
	if [ "$where" = lane ]; then cd "$tmp/owner/.worktrees/03"; else cd "$tmp/owner"; fi
	if ! sh -c "$*" >/dev/null 2>&1; then desc="$desc (failed)"; fi
	if [ -s "$log" ]; then
		while IFS= read -r line; do printf '%s\t%s\t%s\t%s\n' "$ver" "$where" "$desc" "$line"; done < "$log"
	else
		printf '%s\t%s\t%s\t(no hook call)\t\t\t\t\t\n' "$ver" "$where" "$desc"
	fi
	cd "$tmp/owner"
}

G=$GIT
for w in lane owner; do
	b=$w-branch
	record $w "commit"                 "echo $w >> f && $G add f && $G commit -q -m c"
	record $w "commit --no-verify"     "echo $w >> f && $G add f && $G commit -q --no-verify -m c"
	record $w "commit --amend"         "$G commit -q --amend -m amended"
	record $w "branch create"          "$G branch $b"
	record $w "branch -f (other)"      "$G branch -f $b HEAD~1"
	record $w "branch -D (other)"      "$G branch -D $b"
	record $w "tag"                    "$G tag t-$w"
	record $w "tag -d"                 "$G tag -d t-$w"
	record $w "checkout -b"            "$G checkout -q -b $w-topic"
	record $w "checkout back"          "$G checkout -q -"
	record $w "switch --detach"        "$G switch -q --detach HEAD"
	record $w "switch back"            "$G switch -q -"
	record $w "reset --hard HEAD~1"    "$G reset -q --hard HEAD~1"
	record $w "stash push"             "echo s >> f && $G stash -q"
	record $w "stash pop"              "$G stash pop -q"
	record $w "stash drop (after push)" "$G stash -q && $G stash drop -q"
	record $w "cherry-pick"            "$G cherry-pick main~0 || $G cherry-pick --abort"
	record $w "merge --ff-only main"   "$G merge -q --ff-only main"
	record $w "rebase main"            "$G rebase -q main"
	record $w "fetch"                  "$G fetch -q origin"
	record $w "push (own branch)"      "$G push -q origin HEAD"
	record $w "update-ref other branch" "$G update-ref refs/heads/main HEAD"
	record $w "update-ref refs/aw"     "$G update-ref refs/aw/archive/x HEAD"
	record $w "symbolic-ref HEAD"      "$G symbolic-ref HEAD refs/heads/main"
	record $w "symbolic-ref back"      "$G checkout -q -"
	record $w "bisect start/reset"     "$G bisect start HEAD HEAD~1 && $G bisect reset"
	record $w "notes add"              "$G notes add -m n HEAD"
	record $w "pack-refs --all"        "$G pack-refs --all"
	record $w "gc --auto"              "$G gc -q --auto"
done
record owner "worktree add (new branch)" "$G worktree add -q -b lane-04 .worktrees/04"
record owner "worktree remove"           "$G worktree remove .worktrees/04"
record owner "branch -D lane-04"         "$G branch -D lane-04"
