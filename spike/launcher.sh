#!/bin/sh
# Prototype of bin/aw, to the shape in distribution.md. Steps 1 to 3 use only shell builtins.
[ -n "${AW_TOOL_PATH:-}" ] && exec "$AW_TOOL_PATH" "$@"
case $0 in
*/bin/aw) root=${0%/bin/aw} ;;
bin/aw) root=. ;;
*) echo "aw: cannot find the workspace from $0" >&2; exit 2 ;;
esac
# 1. The owner checkout: a lane root points to it; otherwise this is it.
owner=$root
if [ -f "$root/.aw/owner" ]; then
	IFS= read -r owner < "$root/.aw/owner" || [ -n "$owner" ]
fi
# 2. The pin. (The checksum is needed only on a cache miss, below.)
IFS= read -r version < "$owner/.aw/version" || [ -n "$version" ]
# 3. The cached binary.
bin=${XDG_CACHE_HOME:-$HOME/.cache}/agent-workspace/bin/$version/aw
[ -x "$bin" ] && exec "$bin" "$@"
# 4. Fetch and verify: not part of the spike.
echo "aw: $version is not cached" >&2
exit 2
