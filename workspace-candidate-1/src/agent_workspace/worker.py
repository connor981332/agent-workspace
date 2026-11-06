"""Detached setup worker; receives only its setup lock, never the maintenance gate."""
import fcntl
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
from agent_workspace.context import Context
from agent_workspace.operations import Operations


if __name__ == '__main__':
    context = Context.discover(sys.argv[1])
    try:
        with context.gate_path.open('r') as gate:
            fcntl.flock(gate, fcntl.LOCK_SH)
            Operations(context).setup(int(sys.argv[2]))
    finally:
        os.close(int(sys.argv[3]))
