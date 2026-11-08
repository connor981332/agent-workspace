#!/bin/sh
# Runs on the host before VS Code or the devcontainer CLI starts the container.
# The real work (claiming the container, the shared network, and the shared
# infrastructure) lives in the workspace repo this repo is cloned into.

set -eu

INIT="../bin/devcontainer-init"
if [ ! -f "$INIT" ]; then
  echo "This repo must be checked out inside the {{TITLE}} workspace ({{CHECKOUT}})." >&2
  exit 1
fi
exec /bin/sh "$INIT"
