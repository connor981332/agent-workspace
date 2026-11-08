#!/bin/sh
# Checks a GoReleaser build in dist/: the four raw binaries are listed under their release names,
# checksums.txt verifies them, they are static, and the linux/amd64 one reports its version.
# Usage: check-release-assets.sh [dist-dir]
set -eu
dist=${1:-dist}
assets=$dist/assets
rm -rf "$assets"
mkdir -p "$assets"
python3 - "$dist" "$assets" <<'PY'
import json, shutil, sys
dist, assets = sys.argv[1], sys.argv[2]
names = []
for a in json.load(open(f"{dist}/artifacts.json")):
    if a["type"] == "Binary" and a["name"].startswith("aw_"):
        shutil.copy(a["path"], f"{assets}/{a['name']}")
        names.append(a["name"])
platforms = sorted(n.rsplit("_", 2)[1] + "/" + n.rsplit("_", 1)[1] for n in names)
want = ["darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"]
if platforms != want:
    sys.exit(f"release binaries: got {platforms}, want {want}")
PY
cp "$dist/checksums.txt" "$assets/"
cd "$assets"
cat checksums.txt
sha256sum -c checksums.txt
for f in aw_*_linux_*; do
	file "$f" | grep -q 'statically linked' || { echo "$f is not statically linked" >&2; exit 1; }
done
./aw_*_linux_amd64 version --json
