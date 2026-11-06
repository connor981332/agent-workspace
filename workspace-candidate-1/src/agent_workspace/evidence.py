#!/usr/bin/env python3
"""Lane lifecycle evidence and operations. No shell evaluation of persisted data.

The dispatcher holds the maintenance gate (exclusive for apply/allocation). Slow
remote evidence is gathered before the state lock; identities are rechecked there.
"""
import argparse
import contextlib
import datetime as dt
import fcntl
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import uuid
from urllib.parse import urlparse

from .forge import for_identity

SHA = re.compile(r"[0-9a-f]{40,64}\Z")
GENERATED = re.compile(r"agent/[1-9][0-9]*-[0-9]{8}-[0-9]{4,6}(?:-[0-9a-f]{8})?\Z")


def run(args, cwd=None):
    try:
        return subprocess.run([str(x) for x in args], cwd=cwd, capture_output=True,
                              text=True, timeout=30, env={**{k: v for k, v in os.environ.items() if k != 'AW_LIFECYCLE_GATE_FD'}, 'GIT_OPTIONAL_LOCKS': '0'})
    except (OSError, subprocess.TimeoutExpired) as e:
        return subprocess.CompletedProcess(args, 127, '', str(e))


def git(repo, *args):
    return run(['git', '-C', repo, *args])


def value(repo, *args):
    r = git(repo, *args)
    return r.stdout.strip() if r.returncode == 0 else None


def atomic(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix='.lifecycle-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
            f.write('\n')
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
        d = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(d)
        finally:
            os.close(d)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


@contextlib.contextmanager
def lock(path):
    with path.open('a') as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        yield


def locked_file(path):
    """Probe an existing lock without creating/truncating it. Errors protect."""
    if not path.exists():
        return path.is_symlink()
    try:
        with path.open('r') as f:
            fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return False
    except OSError:
        return True


def contained(path, root):
    return path.is_relative_to(root) and path.resolve() == path


def remote_identity(url):
    if not url:
        return None
    if '://' in url:
        p = urlparse(url)
        host, project = p.hostname, p.path.lstrip('/')
    else:
        m = re.fullmatch(r'(?:[^@/]+@)?([^:/]+):(.+)', url)
        if not m:
            return None
        host, project = m.groups()
    project = project.removesuffix('.git')
    return {'host': host, 'project': project} if host and '/' in project else None


class Evidence:
    def __init__(self, main, lanes, bases, caller=None, remote=False, namespace='aw', forge='auto'):
        self.main, self.lanes = Path(main), Path(lanes)
        self.namespace, self.forge_setting = namespace, forge
        self.forges = {}
        self.state = self.lanes / ('.' + namespace)
        self.bases = bases  # stable identity -> (relative directory, configured base)
        self.caller, self.remote = caller, remote
        self.fetches = {}
        self.remote_deadline = time.monotonic() + 15
        prefix = namespace.upper() + '_'
        self.pool = int(os.environ.get(prefix + 'LANE_POOL', '2'))
        self.days = int(os.environ.get(prefix + 'LANE_RETENTION_DAYS', '14'))
        if self.pool < 0 or self.days < 1:
            raise ValueError('pool must be nonnegative and retention at least one day')

    def marker_text(self, n):
        return f'{self.namespace.upper()}_LANE={n}\n'

    def source(self, repo, base):
        """Map configured bases, never the owner's checked-out branch."""
        ref = value(repo, 'rev-parse', '--symbolic-full-name', base)
        if not ref:
            return None
        if ref.startswith('refs/remotes/'):
            remote, _, target = ref[len('refs/remotes/'):].partition('/')
        elif ref.startswith('refs/heads/'):
            branch = ref[len('refs/heads/'):]
            remote = value(repo, 'config', f'branch.{branch}.remote')
            target = value(repo, 'config', f'branch.{branch}.merge')
            if target:
                target = target.removeprefix('refs/heads/')
            # An untracked local base can map only to an equal named remote ref.
            if not remote:
                candidates = [r for r in (value(repo, 'remote') or '').splitlines()
                              if value(repo, 'rev-parse', '--verify', f'refs/remotes/{r}/{branch}')]
                if len(candidates) != 1:
                    return None
                remote, target = candidates[0], branch
        else:
            return None
        identity = remote_identity(value(repo, 'remote', 'get-url', remote or ''))
        if not identity or not target or remote == '.':
            return None
        return {**identity, 'remote': remote, 'target': target}

    def push_state(self, repo, source, branch, head):
        if not source or not branch or not head:
            return 'unknown'
        remote_head = value(repo, 'rev-parse', '--verify', f"refs/remotes/{source['remote']}/{branch}")
        if not remote_head:
            return 'source ref missing (not proof of merge or unpushed work)'
        if git(repo, 'merge-base', '--is-ancestor', head, remote_head).returncode == 0:
            return 'covered by local remote ref'
        ahead = value(repo, 'rev-list', '--count', f'{remote_head}..{head}')
        return 'unpushed' if ahead and int(ahead) else 'unknown'

    def forge_for(self, source):
        if not source:
            return None
        key = (source['host'], source['project'])
        if key not in self.forges:
            self.forges[key] = for_identity(self.forge_setting, source, lambda: self.remote_deadline - time.monotonic())
        return self.forges[key]

    def mr(self, source, branch, head):
        if not self.remote or not source:
            return {'availability': 'unknown (local mode or unmappable base)'}
        forge = self.forge_for(source)
        if not forge:
            return {'availability': 'unknown (no forge adapter for this host)'}
        return forge.change(source, branch, head)

    def proof(self, repo, head, base_sha):
        if not head or not base_sha:
            return None
        if git(repo, 'merge-base', '--is-ancestor', head, base_sha).returncode == 0:
            return 'ancestry'
        # Cherry is not a proof for histories containing merges.
        merges = value(repo, 'rev-list', '--merges', f'{base_sha}..{head}')
        if merges is None or merges:
            return None
        r = git(repo, 'cherry', base_sha, head)
        lines = r.stdout.splitlines()
        if r.returncode == 0 and lines and all(re.fullmatch(r'- [0-9a-f]{40,64}', x) for x in lines):
            return 'patch equivalence'
        return None

    def repo(self, identity, path, branch=None):
        base = self.bases[identity][1]
        out = {'identity': identity, 'path': str(path), 'base_ref': base, 'errors': []}
        if not contained(path, self.main) or not path.exists():
            return {**out, 'errors': ['missing or unsupported/symlink path']}
        out['head'] = value(path, 'rev-parse', '--verify', branch or 'HEAD')
        out['branch'] = branch or value(path, 'symbolic-ref', '--quiet', '--short', 'HEAD')
        out['base_sha'] = value(path, 'rev-parse', '--verify', base + '^{commit}')
        status = git(path, 'status', '--porcelain=v1', '-uall')
        out['status'] = status.stdout.splitlines()
        if status.returncode:
            out['errors'].append('status unreadable: ' + status.stderr.strip())
        if not out['head'] or not out['branch']:
            out['errors'].append('detached or invalid HEAD/branch')
        if not out['base_sha']:
            out['errors'].append('configured base unavailable')
        expected = self.main / self.bases[identity][0]
        if value(path, 'rev-parse', '--path-format=absolute', '--git-common-dir') != value(expected, 'rev-parse', '--path-format=absolute', '--git-common-dir'):
            out['errors'].append('wrong repository registration')
        out['source'] = self.source(path, base)
        out['merge_proof'] = self.proof(path, out['head'], out['base_sha'])
        out['mr'] = self.mr(out['source'], out['branch'], out['head']) if out['branch'] else {'availability': 'unknown'}
        if out['mr'].get('state') == 'merged' and not out['merge_proof']:
            out['merge_proof'] = self.forge_for(out['source']).label + ' exact source head'
        out['push'] = self.push_state(path, out['source'], out['branch'], out['head'])
        return out

    def handoff(self, n):
        path = self.state / 'lanes' / str(n) / 'handoff'
        if not path.exists():
            return None, None
        try:
            if not contained(path, self.state):
                raise ValueError('symlink handoff')
            data = json.loads(path.read_text(encoding='utf-8'))
            if not isinstance(data, dict) or data.get('schema_version') not in (1, 2) or type(data.get('lane')) is not int or data['lane'] != n:
                raise ValueError('unsupported schema or lane')
            if data['schema_version'] == 2:
                reason = data.get('reason')
                if (data.get('delivery') != 'local' or not isinstance(reason, str)
                        or not reason.strip() or '\n' in reason or '\r' in reason):
                    raise ValueError('invalid local delivery/reason')
            elif data.get('delivery', 'mr') != 'mr' or data.get('reason') is not None:
                raise ValueError('invalid legacy delivery')
            if not all(k in data for k in ('summary', 'finished_at', 'plan', 'repos')):
                raise ValueError('missing required fields')
            if not isinstance(data.get('summary'), str) or not data['summary'].strip() or '\n' in data['summary'] or '\r' in data['summary']:
                raise ValueError('invalid summary')
            if not isinstance(data.get('finished_at'), str) or not data['finished_at'].endswith('Z'):
                raise ValueError('invalid finished_at')
            dt.datetime.fromisoformat(data['finished_at'].replace('Z', '+00:00'))
            self.plan(n, data.get('plan'), allow_missing=True)
            repos = data['repos']
            if not isinstance(repos, list) or len(repos) != len(self.bases):
                raise ValueError('invalid repositories')
            seen = set()
            for r in repos:
                if not isinstance(r, dict) or r.get('identity') not in self.bases or r['identity'] in seen:
                    raise ValueError('invalid/duplicate repository')
                seen.add(r['identity'])
                if any(not isinstance(r.get(k), str) or not SHA.fullmatch(r[k]) for k in ('head', 'base_sha')):
                    raise ValueError('invalid SHA')
                if any(not isinstance(r.get(k), str) or not r[k] for k in ('branch', 'base_ref')):
                    raise ValueError('invalid branch/base')
                if not isinstance(r.get('source'), (dict, type(None))):
                    raise ValueError('invalid source')
                if r['source'] is not None and any(not isinstance(r['source'].get(k), str) or not r['source'][k] for k in ('host', 'project', 'remote', 'target')):
                    raise ValueError('invalid remote identity')
                mr = r.get('mr')
                if mr is not None and (not isinstance(mr, dict) or type(mr.get('iid')) is not int or
                        mr['iid'] < 1 or
                        any(not isinstance(mr.get(k), str) or not mr[k] for k in ('project', 'url', 'source_head')) or
                        mr['source_head'] != r['head'] or not r['source'] or mr['project'] != r['source']['project']):
                    raise ValueError('invalid MR')
            return data, None
        except (OSError, ValueError, KeyError, TypeError) as e:
            return None, 'invalid handoff: ' + str(e)

    def plan(self, n, name, allow_missing=False):
        if name is None:
            return None
        if not isinstance(name, str) or not name or Path(name).is_absolute() or '..' in Path(name).parts or Path(name).as_posix() != name:
            raise ValueError('plan must be a contained workspace-relative path')
        path = self.lanes / str(n) / name
        if not contained(path, self.lanes / str(n)):
            raise ValueError('plan escapes lane')
        if allow_missing and not path.exists():
            return None
        if not path.is_file() or not name.startswith('plans/') or path.suffix != '.md':
            raise ValueError('plan must name an existing plans/*.md file')
        text = path.read_text(encoding='utf-8')
        if any(s not in text for s in ('## Status', '**State:**', '**Updated:**', '**Next step:**', '**In progress:**')):
            raise ValueError('plan lacks structured Status')
        return text

    def protections(self, n):
        s = self.state / 'lanes' / str(n)
        reasons = []
        # Read the common attachment reservation format without invoking its
        # liveness refresh (inspection must not rewrite records). This is also
        # safe when records are unreadable: unknown records protect all.
        attachments = self.state / 'attachments'
        if not contained(attachments, self.state):
            reasons.append('uncertain attachment ownership/symlink')
        elif attachments.exists():
            try:
                for path in attachments.glob('*.json'):
                    if not contained(path, self.state):
                        reasons.append('uncertain attachment ownership/symlink')
                        continue
                    try:
                        data = json.loads(path.read_text())
                        if not isinstance(data, dict) or data.get('schema_version') != 1 or type(data.get('lane')) is not int or data['lane'] < 1 or data.get('state') not in ('Attaching', 'Attached', 'Uncertain', 'Ended'):
                            raise ValueError('unknown attachment format')
                        if data['state'] != 'Ended' and data['lane'] == n:
                            reasons.append('attachment ownership: ' + data['state'])
                    except (OSError, ValueError, TypeError):
                        reasons.append('uncertain attachment ownership: ' + path.name)
            except OSError:
                reasons.append('unreadable attachment ownership')
        if not contained(s, self.state):
            return reasons + ['uncertain state ownership/symlink']
        if self.caller == n:
            reasons.append('caller lane')
        try:
            if not s.is_dir():
                return reasons + ['missing session state; ownership unknown']
            for name in ('session.lock', 'setup.lock', 'operation.lock'):
                if locked_file(s / name):
                    reasons.append(name + ' held or unreadable')
            if not (s / 'session.lock').is_file():
                reasons.append('session ownership unknown (no session.lock)')
            for name in ('claimed', 'reserved', 'parked'):
                if (s / name).exists() or (s / name).is_symlink():
                    reasons.append(name)
            for f in s.glob('busy.*'):
                try:
                    os.kill(int(f.name.split('.')[1]), 0)
                    reasons.append(f.name)
                except ProcessLookupError:
                    pass
                except (ValueError, OSError):
                    reasons.append('uncertain busy command: ' + f.name)
            # Servers with no session can still contain work: never guess PID ownership.
            if list(s.glob('*.pid')):
                reasons.append('server ownership requires slot release')
        except OSError as e:
            reasons.append('unreadable state: ' + str(e))
        return reasons

    def activity(self, n, handoff):
        s = self.state / 'lanes' / str(n)
        stamps = []
        for name in ('activity', 'last', 'api.log', 'next.log'):
            try:
                p = s / name
                if contained(p, self.state) and p.is_file():
                    stamps.append((p.stat().st_mtime, name))
            except OSError:
                pass
        if handoff:
            stamps.append((dt.datetime.fromisoformat(handoff['finished_at'].replace('Z', '+00:00')).timestamp(), 'handoff.finished_at'))
        return max(stamps) if stamps else (None, 'unknown')

    def registrations(self, identity):
        path = self.main / self.bases[identity][0]
        r = git(path, 'worktree', 'list', '--porcelain', '-z')
        if r.returncode:
            return {}, r.stderr.strip()
        result, entry = {}, {}
        for part in r.stdout.split('\0'):
            if not part:
                if 'worktree' in entry:
                    result[entry['worktree']] = entry
                entry = {}
            else:
                k, _, v = part.partition(' ')
                entry[k] = v or True
        return result, None

    def lane(self, n, registrations=None):
        path = self.lanes / str(n)
        reasons = self.protections(n)
        try:
            marker = path / '.lane'
            if not contained(marker, self.lanes) or marker.read_text() != self.marker_text(n):
                reasons.append('invalid lane marker')
        except OSError:
            reasons.append('missing/unreadable lane marker')
        repos = []
        for identity, (rel, _) in self.bases.items():
            p = path / rel
            regs, err = registrations[identity] if registrations else self.registrations(identity)
            registration = regs.get(str(p))
            if err or not registration or any(k in registration for k in ('locked', 'prunable')):
                reasons.append(f'{identity}: locked/missing/invalid registration {err or ""}')
            repos.append(self.repo(identity, p))
        handoff, error = self.handoff(n)
        if error:
            reasons.append(error)
        current = handoff and all(any(r.get('identity') == h['identity'] and r.get('head') == h['head'] and
            r.get('branch') == h['branch'] and r.get('base_ref') == h['base_ref'] and r.get('source') == h['source']
            for r in repos) for h in handoff['repos'])
        if handoff and not current:
            reasons.append('stale handoff (heads changed)')
        dirty = any(r.get('status') or r.get('push') == 'unpushed' for r in repos)
        errors = any(r['errors'] for r in repos)
        if errors or error:
            cls = 'Unknown'
        elif dirty:
            cls = 'WIP'
        elif all(r.get('merge_proof') for r in repos):
            cls = 'Merged'
        elif current and all(r.get('merge_proof') or r['mr'].get('state') == 'opened' for r in repos):
            cls = 'Awaiting review'
        else:
            cls = 'Interrupted'
        if reasons:
            underlying = cls
            cls = 'Parked' if reasons == ['parked'] else 'Active / reserved' if any('lock' in x or x in ('claimed', 'reserved', 'caller lane') or 'ownership' in x for x in reasons) else 'Unknown'
        else:
            underlying = cls
        stamp, source = self.activity(n, handoff)
        age = max(0, time.time() - stamp) if stamp is not None else None
        abandoned = age is not None and age >= self.days * 86400 and underlying != 'Merged' and all(
            r.get('merge_proof') or r['mr'].get('state') == 'closed' or r['mr']['availability'] == 'confirmed absent' for r in repos)
        next_step = 'unavailable (no declared plan)'
        if handoff and handoff.get('plan'):
            text = self.plan(n, handoff['plan'], allow_missing=True)
            m = re.search(r'\*\*Next step:\*\*\s*(.*)', text or '')
            next_step = m.group(1) if m else 'unavailable (declared plan missing)'
        return {'kind': 'lane', 'lane': n, 'path': str(path), 'class': cls, 'underlying': underlying,
                'protection': reasons, 'repos': repos, 'age_seconds': age, 'age_source': source,
                'abandoned_question': abandoned, 'next_step': next_step,
                'action': 'keep', 'handoff': handoff,
                'resume': f'bin/aw resume {n}' if underlying in ('Interrupted', 'WIP') else None,
                'plan_hints': (value(path, 'ls-files', 'plans/*.md') or '').splitlines() if not handoff and contained(path, self.lanes) and path.is_dir() else []}

    def inventory(self, lanes_only=False):
        regs = {i: self.registrations(i) for i in self.bases}
        entries = []
        numbers = set()
        if self.lanes.exists():
            numbers.update(int(p.name) for p in self.lanes.iterdir() if re.fullmatch(r'[1-9][0-9]*', p.name))
        for registrations, _ in regs.values():
            for name in registrations:
                p = Path(name)
                if p.is_relative_to(self.lanes):
                    part = p.relative_to(self.lanes).parts
                    if part and re.fullmatch(r'[1-9][0-9]*', part[0]):
                        numbers.add(int(part[0]))
        entries += [self.lane(n, regs) for n in sorted(numbers)]
        for journal in (self.state / 'history').glob('*.json'):
            try:
                data = json.loads(journal.read_text())
                if data.get('status') in ('failed', 'in progress'):
                    entries.append({'kind': 'journal', 'class': 'Partial failure', 'path': str(journal),
                                    'action': 'report only', 'error': data.get('error'),
                                    'recovery': 'bin/aw sweep --apply --retry ' + data['operation'] if data.get('entry', {}).get('kind') == 'lane' else 'inspect branch evidence; rerun sweep'})
            except (OSError, ValueError, KeyError, AttributeError):
                entries.append({'kind': 'journal', 'class': 'Unknown', 'path': str(journal), 'action': 'report only'})
        eligible = sorted([e for e in entries if e['class'] == 'Merged' and not e['protection']],
                          key=lambda e: (-(time.time() - e['age_seconds']) if e['age_seconds'] is not None else float('inf'), e['lane']))
        for index, e in enumerate(eligible):
            expired = e['age_seconds'] is not None and e['age_seconds'] >= self.days * 86400
            e['action'] = 'reclaim' if expired or index >= self.pool else 'keep warm'
        if lanes_only:
            return entries
        registered = set()
        for identity, (items, error) in regs.items():
            if error:
                entries.append({'kind': 'error', 'identity': identity, 'class': 'Unknown', 'action': 'report only', 'error': error})
            for name, registration in items.items():
                p = Path(name)
                registered.add(p)
                if p.is_relative_to(self.lanes):
                    continue
                if not contained(p, self.main / '.worktrees') or p.parent != self.main / '.worktrees' or any(k in registration for k in ('locked', 'prunable')):
                    entries.append({'kind': 'worktree', 'path': name, 'identity': identity, 'class': 'Protected', 'registration': registration, 'action': 'report only'})
                    continue
                repo = self.repo(identity, p)
                safe = not repo['errors'] and not repo['status'] and bool(repo.get('merge_proof'))
                entries.append({'kind': 'worktree', 'path': name, 'identity': identity, 'class': 'Stale worktree' if safe else 'Unknown / WIP', 'repo': repo, 'registration': registration, 'action': 'remove worktree' if safe else 'report only'})
            primary = self.main / self.bases[identity][0]
            checked = {v.get('branch', '').removeprefix('refs/heads/') for v in items.values() if isinstance(v.get('branch', ''), str)}
            base_branch = value(primary, 'rev-parse', '--symbolic-full-name', self.bases[identity][1])
            branches = value(primary, 'for-each-ref', '--format=%(refname:short)', 'refs/heads') or ''
            for branch in branches.splitlines():
                head = value(primary, 'rev-parse', branch)
                base = value(primary, 'rev-parse', self.bases[identity][1])
                proof = self.proof(primary, head, base)
                source = self.source(primary, self.bases[identity][1])
                mr = self.mr(source, branch, head)
                push = self.push_state(primary, source, branch, head)
                if not proof and mr.get('state') == 'merged':
                    proof = self.forge_for(source).label + ' exact source head'
                protected = branch in checked or base_branch == 'refs/heads/' + branch or branch in ('master', 'main') or push == 'unpushed'
                entries.append({'kind': 'branch', 'identity': identity, 'branch': branch, 'head': head,
                                'base_ref': self.bases[identity][1], 'base_sha': base, 'merge_proof': proof, 'mr': mr, 'push': push,
                                'class': 'Protected' if protected else 'Stale branch' if proof else 'Unknown',
                                'action': 'delete branch (-d)' if proof and not protected else 'report only'})
        root = self.main / '.worktrees'
        if root.is_dir() and not root.is_symlink():
            for p in sorted(root.iterdir()):
                if p not in registered:
                    entries.append({'kind': 'unknown', 'path': str(p), 'class': 'Backups / unknown', 'action': 'report only'})
        elif root.exists() or root.is_symlink():
            entries.append({'kind': 'unknown', 'path': str(root), 'class': 'Backups / unknown', 'action': 'report only'})
        return entries
