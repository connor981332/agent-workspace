"""Agent workflows: allocation, setup, launch, attachment and maintenance."""
from contextlib import nullcontext
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import time
import uuid

from . import attachments
from .context import git, command, lane_id
from .evidence import atomic, lock, locked_file
from .lifecycle import Lifecycle
from .runtime import Runtime, checked, forward, write_text


def require_projects(context):
    """Refuse before allocating anything when bootstrap has not cloned the projects."""
    missing = [d for d, _ in context.nested_projects if not (context.main / d / '.git').exists()]
    if missing:
        raise ValueError('Project repositories missing from ' + str(context.main) + ': ' + ', '.join(missing)
                         + '. The owner runs bin/aw bootstrap there, then retries.')


class Operations:
    def __init__(self, context):
        self.c = context
        self.runtime = Runtime(context)
        manifest = context.manifest
        self.life = Lifecycle(context.main, context.lanes, context.bases, context.caller, True,
                              manifest.namespace, manifest.forge)
        self.life.generated = {p.directory if p.nested else '': p.generated for p in manifest.projects.values()}
        self.life.pool, self.life.days = context.settings['lane_pool'], context.settings['lane_retention_days']
        self.life.runtime = self.runtime

    def reconcile_sessions(self, number=None, already_locked=None):
        """Operational reconciliation; unlike reporting, this may persist positive end evidence."""
        for path in (self.c.state / 'attachments').glob('*.json'):
            if path == already_locked or path.is_symlink():
                continue
            with lock(path.with_suffix('.lock')):
                data = attachments.load(path)
                if data and (number is None or data['lane'] == number):
                    attachments.refreshed(path, data)

    def invalidate(self, n):
        s = self.c.lane_state(n)
        with lock(self.c.state / 'lock'):
            if (s / 'handoff').exists():
                handoff, error = self.life.handoff(n)
                operation = uuid.uuid4().hex
                atomic(self.c.state / 'history' / (operation + '-resume.json'), dict(
                    schema_version=1, operation=operation, created_at=time.time(),
                    status='report only' if error else 'complete', entry=dict(kind='resume', lane=n), handoff=handoff, error=error))
                (s / 'handoff').unlink()
            (s / 'activity').touch()

    def require_available(self, n, allow_claim=False):
        self.reconcile_sessions(n)
        reasons = [r for r in self.life.protections(n) if r not in
                   ('parked', 'server ownership requires slot release') and not (allow_claim and r == 'claimed')]
        if reasons:
            raise ValueError('Lane protected: ' + '; '.join(reasons))

    def allocate(self, harness=None, session=None, pid=None):
        require_projects(self.c)
        self.reconcile_sessions(already_locked=attachments.path_for(self.c.state, harness, session) if harness else None)
        report = self.life.sweep(True, True, True)
        with lock(self.c.state / 'lock'):
            n = report['reuse']
            if n is None:
                n = next((i for i in range(1, self.c.settings['max_lanes']+1)
                          if not self.c.lane(i).exists() and not any((self.c.lane_state(i) / x).exists() for x in ('reserved', 'claimed'))), None)
            if n is None:
                raise ValueError('No lane is free; inspect bin/aw recap')
            s = self.c.lane_state(n)
            s.mkdir(parents=True, exist_ok=True)
            if (s / 'reserved').exists() or (s / 'claimed').exists():
                raise ValueError('Lane was reserved during allocation')
            (s / 'claimed').touch()
            generation = secrets.token_hex(8)
            write_text(s / 'generation', generation + '\n')
            if harness:
                attachments.begin(self.c.state, harness, session, n, generation, pid)
        try:
            branch = f'agent/{n}-' + time.strftime('%Y%m%d-%H%M%S') + '-' + secrets.token_hex(4)
            if self.c.lane(n).exists():
                reasons = [r for r in self.life.protections(n) if r != 'claimed' and
                           not (harness and r == 'attachment ownership: Attaching')]
                if reasons:
                    raise ValueError('Lane protected: ' + '; '.join(reasons))
                for r in self.life.lane(n)['repos']:
                    if r['errors'] or r['status'] or not r.get('merge_proof'):
                        raise ValueError('Lane lacks clean current merge proof')
                self.invalidate(n)
                for identity, (relative, base) in self.c.bases.items():
                    path = self.c.lane(n) / relative
                    old = self.life.repo(identity, path)
                    checked(['git', '-C', path, 'switch', '-q', '-c', branch, base])
                    self.life.delete_branch(old)
            else:
                for identity, (relative, base) in self.c.bases.items():
                    checked(['git', '-C', self.c.main / relative, 'worktree', 'add', '-q', '-b', branch, self.c.lane(n) / relative, base])
                    if identity == 'workspace':
                        write_text(self.c.lane(n) / '.lane', self.c.marker(n))
            # An allocated checkout must carry the launcher before it can write.
            if not (self.c.lane(n) / 'bin/aw').is_file():
                raise ValueError('Configured base lacks bin/aw; run aw init and commit it before allocation')
            (s / 'fresh-db').touch()
            (s / 'session.lock').touch()
            (s / 'activity').touch()
            self.start_setup(n)
            if harness:
                attachments.cmd_set(self.c.state, harness, session, n, generation, 'Attached')
                (s / 'claimed').unlink()
            return n
        except BaseException:
            # Keep a durable claim/uncertain attachment if worktrees were partially created.
            if harness:
                attachments.cmd_set(self.c.state, harness, session, n, generation, 'Uncertain')
            raise

    def start_setup(self, n):
        state = self.c.lane_state(n)
        handle = (state / 'setup.lock').open('a+')
        try:
            fcntl.flock(handle, fcntl.LOCK_EX)
            (state / 'setup.ok').unlink(missing_ok=True)
            with (state / 'setup.log').open('w') as log:
                subprocess.Popen([sys.executable, str(Path(__file__).with_name('worker.py')),
                                  str(self.c.root), str(n), str(handle.fileno())],
                                 stdout=log, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                                 pass_fds=(handle.fileno(),), start_new_session=True)
        finally:
            handle.close()

    def script(self, words, path, env, label):
        if words and forward(list(words), cwd=path, env=env):
            raise ValueError(f'Lane {label} script failed')

    def setup(self, n):
        state, path = self.c.lane_state(n), self.c.validate_lane(n)
        self.runtime.ensure_containers()
        manifest = self.c.manifest
        fresh = state / 'fresh-db'
        env = {**self.runtime.script_env(n), 'AW_IN_SETUP': '1', 'AW_FRESH': '1' if fresh.exists() else ''}
        self.script(manifest.prepare, path, env, 'prepare')
        for alias, project in manifest.projects.items():
            if not project.install:
                continue
            stamp, installed = None, state / (alias + '.installed')
            if project.install_stamp:
                stamp = hashlib.sha1((path / project.directory / project.install_stamp).read_bytes()).hexdigest()
            if not stamp or not installed.exists() or installed.read_text().strip() != stamp:
                if self.runtime.execute(n, alias, list(project.install), busy=False):
                    raise ValueError('Dependency installation failed')
                if stamp:
                    write_text(installed, stamp + '\n')
        self.script(manifest.setup, path, env, 'setup')
        fresh.unlink(missing_ok=True)
        (state / 'setup.ok').touch()

    def launch(self, n, harness, args, gate, resume=False):
        path, state = self.c.validate_lane(n), self.c.lane_state(n)
        with (state / 'session.lock').open('a+') as handle:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            (state / 'claimed').unlink(missing_ok=True)
            (state / 'activity').touch()
            gate.close()  # session.lock protects this lane without blocking maintenance elsewhere
            try:
                return forward([harness, *(args or (['--continue'] if resume and harness == 'claude' else []))], cwd=path)
            finally:
                (state / 'activity').touch()
                with self.c.gate():
                    self.runtime.stop(n)

    def resume(self, n, harness, args, gate):
        self.c.validate_lane(n)
        try:
            self.require_available(n)
        except ValueError as exc:
            raise ValueError(f'Lane {n} is not available: {exc}') from exc
        self.invalidate(n)
        (self.c.lane_state(n) / 'parked').unlink(missing_ok=True)
        (self.c.lane_state(n) / 'claimed').touch()
        if not (self.c.lane_state(n) / 'setup.ok').exists():
            self.start_setup(n)
        return self.launch(n, harness, args, gate, True)

    def attach(self, harness, *, serialized=False, allow_allocate=True):
        variable = {'claude': 'CLAUDE_CODE_SESSION_ID', 'codex': 'CODEX_SESSION_ID', 'grok': 'GROK_SESSION_ID'}[harness]
        session = os.environ.get(variable, '')
        if not re.fullmatch(r'[A-Za-z0-9._-]+', session):
            raise ValueError('No usable harness-issued session identity')
        pid = os.environ.get('CLAUDE_PID') if harness == 'claude' else attachments.harness_process('grok') if harness == 'grok' else None
        if harness == 'grok' and pid is None:
            raise ValueError('Could not find the grok process; its liveness cannot be tracked')
        path = attachments.path_for(self.c.state, harness, session)
        path.parent.mkdir(parents=True, exist_ok=True)
        with nullcontext() if serialized else lock(path.with_suffix('.lock')):
            data = attachments.load(path)
            if data:
                data = attachments.refreshed(path, data)
            if data and data['state'] in attachments.PROTECTED:
                n = data['lane']
                self.c.validate_lane(n)
                if (self.c.lane_state(n) / 'generation').read_text().strip() != data['generation']:
                    raise ValueError('Stale attachment generation; owner must inspect it')
                if pid:
                    attachments.cmd_join(self.c.state, harness, session, data['generation'], pid)
                print(f'Already attached to lane {n}.', file=sys.stderr)
            else:
                if self.c.caller:
                    raise ValueError('This lane belongs to a different or ended session')
                with lock(self.c.state / 'lock'):
                    n = attachments.revive(self.c.state, harness, session, self.c.lanes, pid)
                if n is not None:
                    print(f'Returned to lane {n}.', file=sys.stderr)
                if n is None:
                    if not allow_allocate:
                        raise ValueError('Attachment changed while checking membership; retry attach for exclusive allocation')
                    n = self.allocate(harness, session, pid)
                    print(f'Attached to lane {n}.', file=sys.stderr)
            return attachments.describe(self.c.state, harness, session, self.c.lane(n), self.c)

    def delete(self, n):
        self.require_available(n)
        entry = self.life.lane(n)
        if entry['class'] != 'Merged' or entry['protection']:
            raise ValueError('Deletion requires clean merged work and no ownership uncertainty')
        self.life.apply_entry(entry)

    def containers(self, action):
        if self.c.caller:
            raise ValueError('Container management is owner-only')
        if not self.runtime.docker_projects:
            raise ValueError('No project in this workspace uses the docker runner')
        if action == 'up':
            self.runtime.ensure_containers()
            return
        for path in (self.c.state / 'lanes').glob('*'):
            if path.name.isdigit():
                self.require_available(int(path.name))
        if self.runtime.leases():
            raise ValueError('Release all slots before container maintenance')
        if action == 'down':
            self.runtime.compose('down')
        elif action == 'recreate':
            self.runtime.compose('up', '-d', '--build', '--force-recreate')
            self.runtime.check_mounts()
        else:
            raise ValueError('Use containers up, down or recreate')
