#!/bin/sh
# Run every t_* test in tests/test_*.sh, each in a fresh temporary directory.
#   tests/run.sh [pattern]
set -u
cd "$(dirname "$0")"
. ./lib.sh
for f in test_*.sh; do . "./$f"; done

pattern="${1:-}"
total=0 failed=0
for t in $(grep -ho '^t_[a-z0-9_]*' test_*.sh); do
  case "$t" in *"$pattern"*) ;; *) continue ;; esac
  total=$((total + 1))
  setup_test
  # In a subshell, so a test's cd, exports, and sourced files don't leak.
  # fail() leaves $T/.failed; a test that can't continue also exits non-zero.
  ( "$t" ) || touch "$T/.failed"
  if [ -e "$T/.failed" ]; then
    echo "not ok $t"; failed=$((failed + 1))
  else
    echo "ok     $t"
  fi
  teardown_test
done
echo "$((total - failed))/$total passed"
[ "$failed" -eq 0 ]
