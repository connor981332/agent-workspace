"""Read-only evidence collection and developer attention, independent of cleanup execution."""
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import time

from . import attachments
from .context import command
from .evidence import Evidence, contained, locked_file, value
from .plans import list_plans, read_plan


@dataclass
class Effort:
    identity: str
    title: str
    attention: str
    summary: str
    next_step: str = 'Look at what changed, then resume the lane or discard the work.'
    lanes: list = field(default_factory=list)
    session: str = 'unknown'
    code: str = 'unknown'
    cleanup: str = 'not evaluated'
    source: str = ''
    resume: str | None = None
    command: str | None = None
    quiet: bool = False  # nothing for the owner to do
    warnings: list = field(default_factory=list)


def observe_sessions(state, number=None):
    """Derive liveness without refreshing or rewriting an attachment record."""
    out = []
    directory = Path(state) / 'attachments'
    if directory.is_symlink():
        return [dict(lane=None, state='Uncertain', warning='symlink attachments')]
    for path in sorted(directory.glob('*.json')):
        if path.is_symlink():
            out.append(dict(lane=None, state='Uncertain', warning='symlink attachment'))
            continue
        data = attachments.load(path)
        if data is None:
            out.append(dict(lane=None, state='Uncertain', warning=f'malformed attachment {path.name}'))
            continue
        if number is not None and data['lane'] != number:
            continue
        data = dict(data)
        procs = attachments.bound(data)
        if procs and data['state'] != 'Ended':
            states = [attachments.start_identity(p.get('pid')) for p in procs]
            if any(start is not None and start == p.get('start') for p, start in zip(procs, states)):
                data['state'] = 'Attached'
            elif all(start is None or start != p.get('start') for p, start in zip(procs, states)):
                data['state'] = 'Ended'
        out.append(data)
    return out


class Inspection(Evidence):
    def __init__(self, context, remote=False):
        super().__init__(context.main, context.lanes, context.bases, context.caller, remote)
        self.context = context
        self.pool = context.settings['lane_pool']
        self.days = context.settings['lane_retention_days']

    def protections(self, n):
        reasons = super().protections(n)
        # Recorded and observed liveness are distinct; never persist this reconciliation.
        reasons = [r for r in reasons if not r.startswith('attachment ownership:')]
        for session in observe_sessions(self.state, n):
            if session.get('state') != 'Ended' and session.get('lane') in (None, n):
                reasons.append('attachment ownership: ' + session['state'])
        return reasons

    def entries(self, number=None):
        if self.caller:
            if number not in (None, self.caller):
                raise ValueError('Recap is limited to the caller lane; run the workspace overview owner-side')
            return [self.lane(self.caller)]
        if number is not None:
            return [self.lane(number)]
        return self.inventory()


def recap(context, *, remote=False, number=None, include_all=False, now=None):
    now = time.time() if now is None else now
    inspection = Inspection(context, remote)
    entries = inspection.entries(number)
    sessions = observe_sessions(context.state, context.caller or number)
    efforts, warnings, linked, represented = {}, [], set(), set()
    plan_root = context.root if context.caller else context.main
    plans = {p.path: p for p in list_plans(plan_root)}
    for e in entries:
        if e['kind'] != 'lane':
            continue
        n = e['lane']
        repos = e['repos']
        represented.update((r['identity'], r.get('branch')) for r in repos)
        handoff = e.get('handoff')
        current = bool(handoff and all(any(all(old.get(k) == r.get(k) for k in ('identity', 'head', 'branch', 'base_ref', 'source'))
                       for old in handoff['repos']) for r in repos))
        plan_name = handoff.get('plan') if current else None
        if not plan_name:
            # A Status block's explicit branch reference is evidence; file presence is not.
            declared = [name for name, p in plans.items() if any(
                r.get('branch') and re.search(r'(?<![\w./-])' + re.escape(r['branch']) + r'(?![\w./-])', p.fields['Branches / MRs'])
                for r in repos)]
            if len(declared) == 1:
                plan_name = declared[0]
        plan = plans.get(plan_name)
        notes = []
        if plan_name:
            linked.add(plan_name)
            try:
                local_plan = read_plan(context.lane(n), plan_name)
                if plan and local_plan.fields != plan.fields:
                    notes.append('Lane plan differs from the workspace plan; reconcile recorded next steps.')
                plan = plan or local_plan
            except (OSError, ValueError):
                notes.append('Declared plan missing or unreadable.')
        if handoff and not current:
            notes.append('Handoff heads differ from current work; historical summary is not completion proof.')
        own_sessions = [s for s in sessions if s.get('lane') == n]
        active = any(s.get('state') != 'Ended' for s in own_sessions) or locked_file(context.lane_state(n) / 'session.lock')
        if context.caller == n and any(os.environ.get(key) and s.get('session') == os.environ.get(key) for s in own_sessions
                                     for key in ('CODEX_SESSION_ID', 'CLAUDE_CODE_SESSION_ID', 'GROK_SESSION_ID')):
            active = True
        alive = 'active' if active else 'ended' if own_sessions else 'unknown'
        merged = all(r.get('merge_proof') and not r.get('errors') for r in repos)
        dirty = any(r.get('status') for r in repos)
        code = 'uncommitted changes' if dirty else 'merged' if merged else 'awaiting review' if current else 'unmerged / unknown'
        local_delivery = bool(current and handoff.get('delivery') == 'local' and not merged and not dirty)
        if local_delivery:
            code = 'delivered locally; publication pending'
        title = plan.title if plan else handoff['summary'].split(';', 1)[0] if current else next((r.get('branch') for r in repos if r.get('branch')), f'Lane {n}')
        summary = handoff['summary'] if current else ''
        if merged and not dirty:
            summary = 'Tracked work is covered by current merge evidence.'
        if not summary:
            summaries = []
            for r in repos:
                if r.get('status'):
                    summaries.append(f"{r['identity']}: {len(r['status'])} changed/untracked paths")
                elif not r.get('merge_proof') and not r.get('errors'):
                    subject = value(Path(r['path']), 'log', '-1', '--format=%s')
                    if subject:
                        summaries.append(f"{r['identity']}: {subject}")
            summary = '; '.join(summaries) or 'Clean tracked work is covered by local merge evidence.'
        attention = 'In session' if active else 'Continue' if dirty or not merged else 'Done'
        if plan and plan.fields['State'] != 'FULLY VERIFIED':
            attention = 'In session' if active else 'Verify' if merged else 'Continue'
        if e.get('underlying') == 'Awaiting review':
            attention = 'Review'
        if notes or any(r.get('errors') for r in repos):
            attention = 'Needs triage'
        clean_merged = merged and not dirty
        next_step = (plan.fields['Next step'] if plan else 'No action required for merged tracked work.' if clean_merged
                     else 'A session is running in this lane; no action needed.' if active
                     else 'Look at what changed, then resume the lane or discard the work.')
        if plan and not active and re.search(r'\bthe owner (runs|decides|can do|reviews|checks)\b', next_step, re.I):
            attention = 'Your action' if not notes else 'Needs triage'
        if local_delivery and not notes and not any(r.get('errors') for r in repos):
            attention = 'Your action'
            next_step = 'Review the exact local commits; publication or integration remains an owner action. Reason: ' + handoff['reason']
        cleanup = '; '.join(e.get('protection', [])) or e['action']
        identity = 'plan:' + plan_name if plan else f'lane:{n}:' + str(value(context.lane(n), 'rev-parse', 'HEAD'))
        effort = Effort(identity, title, attention, summary, next_step, [n], alive, code, cleanup,
                        str(plan_root / plan_name) if plan else f'lane {n}: git + handoff/session evidence', warnings=notes)
        effort.quiet = attention == 'Done' or (clean_merged and not plan and not notes and attention == 'In session')
        if not plan and not clean_merged:
            effort.command = f'bin/aw sweep --lane {n} --explain'
        available = not e.get('protection') and not active and not dirty
        harnesses = {s.get('harness') for s in own_sessions if s.get('generation') ==
                     value_generation(context.lane_state(n)) and s.get('harness') in attachments.HARNESSES}
        if available and not merged and len(harnesses) == 1:
            effort.resume = f'bin/aw resume {n} --harness {next(iter(harnesses))}'
        if attention == 'Done' and not include_all and (e.get('age_seconds') is None or e['age_seconds'] > 7 * 86400):
            continue
        if identity in efforts:
            previous = efforts[identity]
            previous.lanes.append(n)
            previous.resume = None
            previous.command = None
            previous.warnings += notes + ['Effort appears in several lanes; inspect before choosing a continuation.']
            if attention != previous.attention:
                previous.attention = 'Needs triage'
                previous.quiet = False
        else:
            efforts[identity] = effort
    for name, plan in plans.items():
        if name in linked:
            continue
        state = plan.fields['State']
        if not include_all and state not in ('PARTIALLY IMPLEMENTED', 'FULLY IMPLEMENTED'):
            continue
        attention = 'Your action' if re.search(r'\bthe owner (runs|decides|can do|reviews|checks)\b', plan.fields['Next step'], re.I) else 'Verify' if state == 'FULLY IMPLEMENTED' else 'Continue'
        efforts['plan:' + name] = Effort('plan:' + name, plan.title, attention,
                                        'Recorded plan; no current session association established.', plan.fields['Next step'],
                                        source=str(plan_root / name))
    for e in entries:
        if e['kind'] == 'journal':
            warnings.append(f"Cleanup recovery: {e.get('error') or e['class']}; {e.get('recovery', e['path'])}")
        if e['kind'] == 'branch' and not e.get('merge_proof') and (e['identity'], e['branch']) not in represented and e['branch'] not in ('main', 'master'):
            key = f"branch:{e['identity']}:{e['branch']}"
            efforts[key] = Effort(key, e['branch'], 'Needs triage', f"Unmerged local {e['identity']} branch; session association unknown.", source='git refs',
                                  next_step='Compare it with the main branch, then merge, resume or delete it.',
                                  command=f"git -C {inspection.bases[e['identity']][0] or '.'} log --oneline -10 {e['branch']}")
        if e['kind'] == 'worktree' and e.get('class') == 'Unknown / WIP':
            key = 'worktree:' + e['path']
            efforts[key] = Effort(key, Path(e['path']).name, 'Needs triage', 'Retained worktree with unmerged or unexplained work.', source=e['path'],
                                  next_step='Check what is uncommitted, then commit it or remove the worktree.',
                                  command=f"git -C {e['path']} status --short")
    if remote and any(r.get('mr', {}).get('availability', '').startswith('unknown')
                      for e in entries for r in e.get('repos', [])):
        warnings.append('Some forge evidence is unavailable or unmappable; local facts remain in use.')
    return dict(schema_version=1, collected_at=datetime.fromtimestamp(now, timezone.utc).isoformat(),
                scope=f'lane {context.caller or number}' if context.caller or number else 'workspace',
                sources=dict(git='local refs (not fetched)', forge='queried' if remote else 'unchecked', plans='recorded claims'),
                efforts=[asdict(x) for x in efforts.values()], warnings=warnings)


def value_generation(state):
    try:
        return (state / 'generation').read_text().strip()
    except OSError:
        return None
