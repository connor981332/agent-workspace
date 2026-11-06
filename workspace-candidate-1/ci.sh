#!/bin/sh
# Everything CI checks for this candidate. Needs Python 3.11+ and Git, and no network.
set -eu
cd "$(dirname "$0")/tests"
exec python3 -B -m unittest test_generic test_lanes test_tooling
