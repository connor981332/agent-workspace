"""Guarded lifecycle mutations over the shared evidence model."""
import contextlib
import datetime as dt
import json
from pathlib import Path
import re
import subprocess
import time
import uuid
from .evidence import Evidence, atomic, lock, locked_file, contained, value, git, run, GENERATED


class Lifecycle(Evidence):
    def refresh(self):
        for identity, (rel, _) in self.bases.items():
            repo = self.main / rel
            remotes = (value(repo, 'remote') or '').splitlines()
            self.fetches[identity] = {}
            for remote in remotes:
                r = git(repo, 'fetch', '--no-prune', remote)
                self.fetches[identity][remote] = 'ok' if not r.returncode else r.stderr.strip()

    def remote_head(self, r):
        src = r.get('source')
        if not src:
            return None
        result = git(r['path'], 'ls-remote', '--exit-code', src['remote'], 'refs/heads/' + r['branch'])
        lines = result.stdout.splitlines()
        return lines[0].split()[0] if result.returncode == 0 and len(lines) == 1 else None

    def finish(self, n, plan, summary, delivery='mr', reason=None):
        if delivery not in ('mr', 'local'):
            raise ValueError('delivery must be mr or local')
        if delivery == 'local':
            if not isinstance(reason, str) or not reason.strip() or '\n' in reason or '\r' in reason:
                raise ValueError('local delivery requires --reason with nonempty one-line text')
        elif reason is not None:
            raise ValueError('--reason applies only to --delivery local')
        errors = []
        if not summary or not summary.strip() or '\n' in summary or '\r' in summary:
            errors.append('summary must be nonempty one-line text')
        try:
            self.plan(n, plan)
        except (ValueError, OSError) as e:
            errors.append(str(e))
        # Local delivery must work offline. Use the same local evidence reader,
        # without turning unavailable network checks into prerequisites.
        remote = self.remote
        try:
            if delivery == 'local':
                self.remote = False
            repos = [self.repo(i, self.lanes / str(n) / rel) for i, (rel, _) in self.bases.items()]
        finally:
            self.remote = remote
        for r in repos:
            errors += [r['identity'] + ': ' + e for e in r['errors']]
            errors += [r['identity'] + ': dirty ' + line for line in r.get('status', [])]
            if delivery == 'mr' and not r.get('merge_proof'):
                if self.remote_head(r) != r.get('head'):
                    errors.append(r['identity'] + ': exact current head unavailable on remote source branch')
                if r['mr'].get('state') != 'opened':
                    errors.append(r['identity'] + ': required open MR unavailable: ' + r['mr']['availability'] +
                                  '; push this exact head and open an MR, or use explicitly authorized '
                                  '--delivery local --reason "..." for local review')
        if errors:
            raise ValueError('Finish prerequisites:\n  ' + '\n  '.join(errors))
        record = {'schema_version': 1, 'lane': n, 'finished_at': dt.datetime.now(dt.timezone.utc).isoformat().replace('+00:00', 'Z'),
                  'summary': summary, 'plan': plan, 'repos': []}
        if delivery == 'local':
            record.update(schema_version=2, delivery='local', reason=reason.strip())
        for r in repos:
            record['repos'].append({k: r[k] for k in ('identity', 'branch', 'head', 'base_ref', 'base_sha', 'source')})
            record['repos'][-1]['mr'] = {k: r['mr'][k] for k in ('project', 'iid', 'url', 'source_head', 'draft')} if r['mr'].get('state') == 'opened' else None
        s = self.state / 'lanes' / str(n)
        if not contained(s, self.state) or not s.is_dir():
            raise ValueError('invalid lane state')
        with lock(self.state / 'lock'), lock(s / 'operation.lock'):
            for r in repos:
                if value(r['path'], 'rev-parse', 'HEAD') != r['head'] or value(r['path'], 'symbolic-ref', '--short', 'HEAD') != r['branch'] or value(r['path'], 'status', '--porcelain=v1', '-uall') != '':
                    raise ValueError('work changed during finish; retry')
            old, error = self.handoff(n)
            if error:
                raise ValueError('Existing handoff requires inspection before replacement: ' + error)
            if old and {k: v for k, v in old.items() if k != 'finished_at'} == {k: v for k, v in record.items() if k != 'finished_at'}:
                record = old
            atomic(s / 'handoff', record)
        # Existing slot release primitives; reservation/session lock is untouched.
        try:
            self.release_slot(n)
        except (ValueError, OSError) as exc:
            raise ValueError('Handoff saved; slot release failed, rerun finish: ' + str(exc)) from exc
        return record

    def journal(self, record):
        atomic(self.state / 'history' / (record['operation'] + '.json'), record)

    def retry(self, operation_id):
        if not re.fullmatch('[0-9a-f]{32}', operation_id):
            raise ValueError('invalid operation ID')
        path = self.state / 'history' / (operation_id + '.json')
        if not contained(path, self.state):
            raise ValueError('invalid journal path')
        op = json.loads(path.read_text())
        if op.get('schema_version') != 1 or op.get('operation') != operation_id:
            raise ValueError('invalid journal schema')
        if op.get('status') == 'complete':
            return op
        e = op['entry']
        n = e['lane']
        if type(n) is not int or n < 1 or n == self.caller or e.get('kind') != 'lane':
            raise ValueError('invalid journal lane')
        s = self.state / 'lanes' / str(n)
        if not contained(s, self.state) or not (s / 'reserved').is_file():
            raise ValueError('recovery reservation missing')
        if [r for r in self.protections(n) if r != 'reserved']:
            raise ValueError('recovery lane is active or protected')
        marker = self.lanes / str(n) / '.lane'
        if marker.exists() and (not contained(marker, self.lanes) or marker.read_text() != self.marker_text(n)):
            raise ValueError('recovery lane marker changed')
        if {r['identity'] for r in e['repos']} != set(self.bases) or len(e['repos']) != len(self.bases):
            raise ValueError('invalid journal repositories')
        remaining = []
        for r in e['repos']:
            p = self.lanes / str(n) / self.bases[r['identity']][0]
            if str(p) != r['path'] or not contained(p, self.lanes):
                raise ValueError('journal path mismatch')
            if not p.exists():
                regs, error = self.registrations(r['identity'])
                if error or str(p) in regs or 'removed ' + str(p) not in op['completed']:
                    raise ValueError('missing path lacks durable removal evidence; manual review required')
                continue
            fresh = self.repo(r['identity'], p)
            if self.fingerprint(fresh) != self.fingerprint(r) or fresh['errors'] or fresh['status'] or not fresh.get('merge_proof'):
                raise ValueError('recovery evidence changed')
            remaining.append(fresh)
        self.removable({'kind': 'lane', 'repos': remaining})
        self.remove_lane(op, remaining)
        return op

    def expire_journals(self):
        # Failed/unknown records are never expired. No credentials are archived.
        for p in (self.state / 'history').glob('*.json'):
            if not contained(p, self.state):
                continue
            try:
                record = json.loads(p.read_text())
                if record.get('schema_version') == 1 and record.get('status') == 'complete' and time.time() - record['created_at'] > 90 * 86400:
                    p.unlink()
            except (OSError, ValueError, KeyError, TypeError, AttributeError):
                pass

    # Directory -> ignored files lane setup generates; regenerable, never hand-written work.
    generated = {}

    def removable(self, e):
        """Plain worktree removal also checks ignored contents. No generic force."""
        repos = e['repos'] if 'repos' in e else [e['repo']]
        for r in repos:
            p = Path(r['path'])
            ignored = git(p, 'ls-files', '--others', '--ignored', '--exclude-standard', '-z')
            allowed = [str(Path(rel)) + '/' for rel, _ in self.bases.values() if rel] if e['kind'] == 'lane' and r['identity'] == 'workspace' else []
            generated = self.generated.get(self.bases[r['identity']][0], ()) if e['kind'] == 'lane' else ()
            # Interpreter bytecode caches are regenerable and never hold work.
            leftovers = [x for x in ignored.stdout.split('\0') if x and not (x == '.lane' and e['kind'] == 'lane' and r['identity'] == 'workspace') and not any(x.startswith(a) for a in allowed)
                         and not any(x == g or (g.endswith('/') and x.startswith(g)) for g in generated)
                         and not (r['identity'] == 'workspace' and '__pycache__' in Path(x).parts[:-1] and x.endswith('.pyc'))]
            if ignored.returncode or leftovers:
                raise ValueError(f"{p}: ignored runtime artifacts require manual review: {leftovers[:10]}")

    def release_slot(self, n):
        # The caller owns the maintenance gate. No subprocess or reentrant gate acquisition.
        self.runtime.stop(n)

    def dispose(self, n):
        # Only called after all worktrees have been removed, so the teardown script comes from
        # the owner checkout. Keep failures journaled.
        runtime = getattr(self, 'runtime', None)
        command = runtime.m.teardown if runtime else ()
        if not command:
            return
        env = {**runtime.script_env(n), 'AW_TOOL': str(self.main / 'bin/aw')}
        try:
            r = subprocess.run(list(command), cwd=self.main, env=env, capture_output=True, text=True, timeout=300)
        except (OSError, subprocess.TimeoutExpired) as e:
            raise ValueError('lane teardown failed: ' + str(e)) from e
        if r.returncode:
            raise ValueError('lane teardown failed: ' + r.stderr)

    def apply_entry(self, e):
        if e['kind'] == 'lane':
            fresh = self.lane(e['lane'])
            if fresh['class'] != 'Merged' or fresh['protection'] or self.fingerprint(fresh) != self.fingerprint(e):
                raise ValueError('lane changed or protected; skipped')
            self.removable(fresh)
            s = self.state / 'lanes' / str(e['lane'])
            with lock(self.state / 'lock'):
                if self.protections(e['lane']):
                    raise ValueError('reservation changed')
                (s / 'reserved').touch()
            operation = {'schema_version': 1, 'operation': uuid.uuid4().hex, 'created_at': time.time(),
                         'entry': e, 'completed': [], 'status': 'in progress'}
            self.journal(operation)
            self.remove_lane(operation, e['repos'])
            if operation.get('retained_branches'):
                e['retained_branches'] = operation['retained_branches']
        elif e['kind'] == 'worktree':
            fresh = self.repo(e['identity'], Path(e['path']))
            regs, error = self.registrations(e['identity'])
            if error or regs.get(e['path']) != e['registration'] or self.fingerprint(fresh) != self.fingerprint(e['repo']) or fresh['errors'] or fresh['status'] or not fresh.get('merge_proof'):
                raise ValueError('worktree changed; skipped')
            self.removable(e)
            result = git(self.main / self.bases[e['identity']][0], 'worktree', 'remove', e['path'])
            if result.returncode:
                raise ValueError(result.stderr.strip())
        elif e['kind'] == 'branch':
            self.delete_branch(e, ordinary=True)

    def remove_lane(self, operation, repos):
        e = operation['entry']
        s = self.state / 'lanes' / str(e['lane'])
        try:
            # Gate -> state -> per-lane lock; release state before Docker calls.
            with contextlib.ExitStack() as stack:
                with lock(self.state / 'lock'):
                    stack.enter_context(lock(s / 'operation.lock'))
                self.release_slot(e['lane'])
                for r in reversed(repos):
                    path = Path(r['path'])
                    if r['identity'] == 'workspace':
                        marker = path / '.lane'
                        if marker.exists():
                            if marker.read_text() != self.marker_text(e['lane']):
                                raise ValueError('invalid lane marker')
                            marker.unlink()
                    result = git(self.main / self.bases[r['identity']][0], 'worktree', 'remove', path)
                    if result.returncode:
                        if r['identity'] == 'workspace' and path.exists():
                            (path / '.lane').write_text(self.marker_text(e['lane']))
                        raise ValueError(result.stderr.strip())
                    operation['completed'].append('removed ' + str(path))
                    self.journal(operation)
                    try:
                        self.delete_branch(r)
                    except ValueError as error:
                        operation.setdefault('retained_branches', []).append(str(error))
                self.dispose(e['lane'])
                operation['completed'].append('disposed data')
                self.journal(operation)
                # Persist complete before clearing reservation: a crash leaves a
                # conservative reservation, never an apparently reusable failure.
                operation['status'] = 'complete'
                operation.pop('error', None)
                self.journal(operation)
                for name in ('handoff', 'claimed', 'setup.ok', 'activity', 'reserved'):
                    (s / name).unlink(missing_ok=True)
                for identity in self.bases:
                    (s / (identity + '.installed')).unlink(missing_ok=True)
        except (OSError, ValueError) as error:
            operation['status'] = 'failed'
            operation['error'] = str(error)
            self.journal(operation)
            raise

    @staticmethod
    def fingerprint(e):
        if 'repos' in e:
            return [Lifecycle.fingerprint(r) for r in e['repos']]
        return tuple(e.get(k) for k in ('identity', 'branch', 'head', 'base_sha', 'merge_proof'))

    def delete_branch(self, r, ordinary=False):
        repo = self.main / self.bases[r['identity']][0]
        regs, error = self.registrations(r['identity'])
        if error or any(x.get('branch') == 'refs/heads/' + r['branch'] for x in regs.values()):
            return 'retained (checked out)'
        if value(repo, 'rev-parse', r['branch']) != r['head']:
            return 'retained (head changed)'
        if value(repo, 'rev-parse', r['base_ref']) != r['base_sha']:
            raise ValueError('base changed; branch retained')
        if not r.get('merge_proof') or r['branch'] in ('main', 'master') or value(repo, 'rev-parse', '--symbolic-full-name', r['base_ref']) == 'refs/heads/' + r['branch']:
            return 'retained (protected)'
        flag = '-D' if not ordinary and GENERATED.fullmatch(r['branch']) else '-d'
        audit = {'schema_version': 1, 'operation': uuid.uuid4().hex, 'created_at': time.time(),
                 'entry': {'kind': 'branch', 'evidence': r, 'flag': flag}, 'status': 'in progress'}
        self.journal(audit)
        result = git(repo, 'branch', flag, r['branch'])
        audit['status'] = 'complete'
        audit['result'] = result.stdout.strip() if not result.returncode else 'retained: ' + result.stderr.strip()
        self.journal(audit)
        if result.returncode:
            raise ValueError('branch retained: ' + result.stderr.strip())
        return result.stdout.strip()

    def sweep(self, apply=False, lanes_only=False, reserve_reuse=False):
        if apply:
            self.refresh()
            self.expire_journals()
        entries = self.inventory(lanes_only)
        reusable = [e for e in entries if e['kind'] == 'lane' and e['class'] == 'Merged' and not e['protection']]
        reusable.sort(key=lambda e: (e['age_seconds'] if e['age_seconds'] is not None else float('inf'), e['lane']))
        reuse = reusable[0] if reserve_reuse and reusable else None
        if reuse:
            reuse['action'] = 'reserve for reuse'
        errors = []
        if apply:
            for e in entries:
                if e['action'] not in ('reclaim', 'remove worktree', 'delete branch (-d)'):
                    continue
                try:
                    self.apply_entry(e)
                    e['result'] = 'complete'
                except (ValueError, OSError) as error:
                    e['result'] = 'retained / partial failure: ' + str(error)
                    errors.append(e['result'])
        return {'entries': entries, 'fetches': self.fetches, 'errors': errors, 'reuse': reuse['lane'] if reuse else None}
