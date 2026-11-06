#!/usr/bin/env python3
"""bin/aw for one workspace. `aw init` copies this file there unchanged.

Each workspace pins its own tool version, so two workspaces on one machine can differ. Resolution:
AW_TOOL_PATH (a source checkout, for working on the tool), then the [tool] pin in workspace.toml,
fetched once into a per-user cache and verified by commit. A guard must never fail open, so any
failure exits 2, which harnesses treat as a blocked tool call.
"""
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import tomllib

ROOT = Path(__file__).resolve().parent.parent
FAIL = 2


def fail(message):
    print('aw: ' + message, file=sys.stderr)
    return FAIL


def cached(source, ref, subdir):
    if not re.fullmatch(r'[0-9a-f]{40}', ref):
        raise ValueError('[tool] ref must be a full commit SHA, so a pin cannot move')
    cache = Path(os.environ.get('XDG_CACHE_HOME') or Path.home() / '.cache') / 'agent-workspace'
    target = cache / ref
    if not (target / '.complete').exists():
        cache.mkdir(parents=True, exist_ok=True)
        temporary = Path(tempfile.mkdtemp(prefix='fetch-', dir=cache))
        try:
            quiet = dict(check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True, timeout=120)
            subprocess.run(['git', 'clone', '-q', '--no-checkout', '--filter=blob:none', source, str(temporary)], **quiet)
            subprocess.run(['git', '-C', str(temporary), 'checkout', '-q', '--detach', ref], **quiet)
            actual = subprocess.run(['git', '-C', str(temporary), 'rev-parse', 'HEAD'], capture_output=True, text=True).stdout.strip()
            if actual != ref:
                raise ValueError(f'fetched {actual}, expected {ref}')
            (temporary / '.complete').touch()
            try:
                temporary.rename(target)
            except OSError:
                if not (target / '.complete').exists():  # A concurrent fetch may have won.
                    raise
        except subprocess.CalledProcessError as exc:
            raise ValueError('Could not fetch the pinned tool: ' + (exc.stderr or str(exc)).strip()) from exc
        finally:
            shutil.rmtree(temporary, ignore_errors=True)
    return target / subdir if subdir else target


def package_root():
    override = os.environ.get('AW_TOOL_PATH')
    if override:
        return Path(override).expanduser().resolve()
    tool = tomllib.loads((ROOT / 'workspace.toml').read_text()).get('tool', {})
    if not tool.get('source') or not tool.get('ref'):
        raise ValueError('workspace.toml has no [tool] source and ref; set them or export AW_TOOL_PATH')
    return cached(tool['source'], tool['ref'], tool.get('subdir', ''))


def main():
    try:
        source = package_root() / 'src'
        if not (source / 'agent_workspace/cli.py').is_file():
            return fail(f'No tool found at {source}')
        sys.path.insert(0, str(source))
        from agent_workspace.cli import main as run
    except (OSError, ValueError, tomllib.TOMLDecodeError, subprocess.SubprocessError, ImportError) as exc:
        return fail(str(exc))
    return run(ROOT, sys.argv[1:])


if __name__ == '__main__':
    sys.exit(main())
