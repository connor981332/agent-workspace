#!/bin/sh
# Everything CI checks for this candidate: the unit suite with stubbed docker
# and real git. Needs only sh, git, awk, and sed; no Docker, no network.
# The end-to-end check (tests/smoke-docker.sh) needs Docker and runs by hand.
set -eu
exec sh "$(dirname "$0")/tests/run.sh"
