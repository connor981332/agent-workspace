"""Workspace identity, declarative settings and explicit lock lifetimes."""
from contextlib import contextmanager
from dataclasses import dataclass
import fcntl
import errno
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import time

from . import manifest as manifest_module


DEFAULTS = dict(slots=2, published_slots=9, port_stride=100, count_owner_servers=1, max_lanes=10,
                slot_idle_minutes=20, evict_grace_seconds=120, workspace_base='main',
                project_base='origin/main', lane_pool=2, lane_retention_days=14)


def command(args, *, cwd=None, timeout=30):
    try:
        return subprocess.run(list(map(str, args)), cwd=cwd, capture_output=True, text=True,
                              timeout=timeout, env={**os.environ, 'GIT_OPTIONAL_LOCKS': '0'})
    except (OSError, subprocess.TimeoutExpired) as exc:
        return subprocess.CompletedProcess(args, 127, '', str(exc))


def git(path, *args):
    result = command(['git', '-C', path, *args])
    if result.returncode:
        raise ValueError(result.stderr.strip() or 'Git inspection failed')
    return result.stdout.strip()


def lane_id(value):
    if not re.fullmatch(r'[1-9][0-9]*', str(value)):
        raise ValueError('Invalid lane number')
    return int(value)


@dataclass
class Context:
    root: Path
    main: Path
    lanes: Path
    caller: int | None
    settings: dict
    projects: dict
    manifest: manifest_module.Manifest

    @classmethod
    def discover(cls, root):
        root = Path(root).resolve()
        common = Path(git(root, 'rev-parse', '--path-format=absolute', '--git-common-dir'))
        main = common.parent
        if common.name != '.git' or not main.is_dir():
            raise ValueError('Unsupported workspace Git registration')
        manifest = manifest_module.load(root)
        data, prefix = manifest.lanes, manifest.env_prefix
        settings = {}
        for key, default in DEFAULTS.items():
            raw = os.environ.get(prefix + key.upper(), data.get(key, default))
            settings[key] = int(raw) if isinstance(default, int) else str(raw)
        if (settings['slots'] < 1 or settings['slots'] > settings['published_slots'] or settings['max_lanes'] < 1
                or settings['lane_pool'] < 0 or settings['lane_retention_days'] < 1
                or settings['slot_idle_minutes'] < 1 or settings['evict_grace_seconds'] < 0
                or settings['count_owner_servers'] not in (0, 1) or settings['port_stride'] < 1):
            raise ValueError(f"Invalid lane limits (published slots are 1-{settings['published_slots']})")
        directory = os.environ.get(prefix + 'LANES_DIR', data.get('directory', str(main / '.lanes')))
        directory = str(directory).replace('$MAIN', str(main))
        lanes = Path(directory)
        if not lanes.is_absolute():
            lanes = main / lanes
        if lanes.resolve() != lanes or lanes.parent != main or not lanes.name.startswith('.') or lanes.name == '.git':
            raise ValueError('Lane directory must be an ignored direct hidden child of the workspace')
        if command(['git', '-C', main, 'check-ignore', '-q', str(lanes) + '/']).returncode:
            raise ValueError('Lane directory must be Git ignored')
        if (lanes / ('.' + manifest.namespace)).is_symlink():
            raise ValueError('Lane state must not be a symlink')
        marker = root / '.lane'
        caller = None
        if marker.exists():
            text = marker.read_text()
            match = re.fullmatch(re.escape(prefix) + r'LANE=([1-9][0-9]*)\n', text)
            if not match or root != lanes / match[1]:
                raise ValueError('Lane marker and canonical registration disagree')
            caller = int(match[1])
        elif root != main:
            raise ValueError('Linked workspace lacks .lane; refusing owner-container routing')
        projects = {p.alias: (p.directory, p.devcontainer_service) for p in manifest.projects.values()}
        return cls(root, main, lanes, caller, settings, projects, manifest)

    @property
    def tool(self):
        return 'bin/aw'

    @property
    def tool_names(self):
        return ('aw', *self.manifest.shims)

    @property
    def nested_projects(self):
        """(directory, alias) of projects that are separate Git repositories."""
        return [(p.directory, p.alias) for p in self.manifest.projects.values() if p.nested]

    def marker(self, number):
        return f'{self.manifest.env_prefix}LANE={number}\n'

    def git_file(self, name):
        """Shared per-workspace metadata lives in .git, so no worktree can commit it."""
        return self.main / '.git' / f'{self.manifest.namespace}-{name}'

    @property
    def state(self):
        return self.lanes / ('.' + self.manifest.namespace)

    @property
    def bases(self):
        prefix = self.manifest.env_prefix
        return {'workspace': ('', self.settings['workspace_base']),
                **{p.alias: (p.directory, os.environ.get(prefix + 'BASE_' + p.alias, p.base or self.settings['project_base']))
                   for p in self.manifest.projects.values() if p.nested}}

    def lane(self, number):
        number = lane_id(number)
        path = self.lanes / str(number)
        if path.resolve() != path:
            raise ValueError('Symlink lane path')
        return path

    def lane_state(self, number):
        path = self.state / 'lanes' / str(lane_id(number))
        if path.resolve() != path:
            raise ValueError('Symlink lane state')
        return path

    def validate_lane(self, number):
        path = self.lane(number)
        if (path / '.lane').read_text() != self.marker(number):
            raise ValueError('Invalid lane marker')
        if git(path, 'rev-parse', '--path-format=absolute', '--git-common-dir') != str(self.main / '.git'):
            raise ValueError('Invalid lane Git registration')
        return path

    @property
    def gate_path(self):
        return self.git_file('lanes.lock')

    def gate_state(self):
        """Observational description of the maintenance gate file."""
        return 'present' if self.gate_path.exists() else 'not created yet'

    def _gate_error(self, path, exc):
        message = f'Cannot open maintenance gate {path}: {exc}'
        if (exc.errno in (errno.EROFS, errno.EACCES, errno.EPERM)
                and (os.environ.get('CODEX_SESSION_ID') or os.environ.get('CODEX_THREAD_ID'))):
            setting = 'sandbox_workspace_write.writable_roots=' + json.dumps([str(self.main / '.git')])
            message += '\nFor Codex workspace-write, restart with: codex -c ' + shlex.quote(setting)
        return ValueError(message)

    @contextmanager
    def gate(self, *, exclusive=False, observe=False, timeout=0):
        """Hold the maintenance gate. Observers yield None when the lock file is absent:
        nobody can hold an absent lock, and observers must never create it."""
        path = self.gate_path
        absent = False
        try:
            handle = path.open('r' if observe else 'a+')
        except OSError as exc:
            if not (observe and exc.errno == errno.ENOENT):
                raise self._gate_error(path, exc) from exc
            absent = True
        if absent:
            yield None
            return
        with handle:
            deadline = time.monotonic() + timeout
            while True:
                try:
                    fcntl.flock(handle, (fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH) | fcntl.LOCK_NB)
                    break
                except BlockingIOError as exc:
                    if time.monotonic() >= deadline:
                        raise ValueError('Lane commands or maintenance are active; retry after they finish') from exc
                    time.sleep(.02)
            yield handle
