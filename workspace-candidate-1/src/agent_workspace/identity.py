"""Focused, observational caller identity. Never refresh attachment/lease state."""
import os
import shlex

from . import attachments
from .context import Context
from .evidence import value


def command_prefix(root):
    return 'cd ' + shlex.quote(str(root)) + ' &&'


def attach_command(payload):
    """Presentation only; never establishes ownership from a harness hint."""
    session = payload.get('session_id') or payload.get('sessionId')
    names = [('claude', 'CLAUDE_CODE_SESSION_ID'), ('codex', 'CODEX_SESSION_ID'), ('grok', 'GROK_SESSION_ID')]
    matches = [h for h, key in names if session and os.environ.get(key) == session]
    if len(matches) == 1:
        harness = matches[0]
    elif 'hookEventName' in payload:
        harness = 'grok'
    else:
        return 'bin/aw attach (add --harness codex or --harness grok for those harnesses)'
    return 'bin/aw attach' + ('' if harness == 'claude' else ' --harness ' + harness)


def whoami(context):
    # Import here: runtime already imports inspection, which uses these readers.
    from .runtime import Runtime
    warnings, matches = [], []
    kind, number, root, session = 'lane' if context.caller else 'owner', context.caller, context.root, None
    for harness, key in (('claude', 'CLAUDE_CODE_SESSION_ID'), ('codex', 'CODEX_SESSION_ID'), ('grok', 'GROK_SESSION_ID')):
        identity = os.environ.get(key)
        if not identity:
            continue
        path = attachments.path_for(context.state, harness, identity)
        if not path.exists() and not path.is_symlink():
            continue
        data = attachments.load(path)
        if not data or data.get('session') != identity or data.get('harness') != harness:
            warnings.append('Current-session attachment is unreadable or invalid; no routing inferred.')
            continue
        if data['state'] == 'Uncertain':
            warnings.append('Current-session attachment ownership is uncertain; inspect before editing.')
        procs = attachments.bound(data)
        if data['state'] == 'Ended' or (procs and not any(
                attachments.start_identity(p['pid']) == p['start'] for p in procs)):
            warnings.append('Current-session attachment has ended; run attach before editing from the owner checkout.')
            continue
        try:
            lane = context.validate_lane(data['lane'])
            generation = (context.lane_state(data['lane']) / 'generation').read_text().strip()
            if generation != data.get('generation'):
                raise ValueError('Stale attachment generation')
            if context.caller and context.caller != data['lane']:
                raise ValueError('Attachment disagrees with caller lane')
            matches.append((lane, data))
        except (OSError, ValueError):
            warnings.append('Current-session attachment is stale or disagrees with the lane; inspect before editing.')
    if len(matches) > 1:
        warnings.append('Ambiguous current-session attachments; no routing inferred.')
    elif len(matches) == 1 and not warnings:
        root, data = matches[0]
        kind = 'lane' if context.caller else 'attached'
        number = data['lane']
        session = {k: data.get(k) for k in ('harness', 'session', 'state', 'generation')}
    if number and root != context.root:
        context = Context.discover(root)
    paths = {'workspace': str(root), **{a: str(root / d) for d, a in context.nested_projects}}
    repos = {}
    for alias, path in paths.items():
        branch = value(path, 'symbolic-ref', '--quiet', '--short', 'HEAD')
        head = value(path, 'rev-parse', '--verify', 'HEAD')
        status = value(path, 'status', '--porcelain=v1', '-uall')
        repos[alias] = dict(branch=branch, head=head, dirty=None if status is None else bool(status))
        if branch is None or head is None or status is None:
            warnings.append(f'{alias}: repository evidence unavailable or detached.')
    slot = dict(number=None, evidence='recorded, unverified', urls=None)
    try:
        leases = Runtime(context).leases()
        matches = [k for k, row in leases.items() if row['holder'] == (f'lane{number}' if number else 'owner')]
        if len(matches) > 1:
            raise ValueError('Multiple recorded slots for caller')
        if matches:
            k = matches[0]
            if not 1 <= k <= context.settings['published_slots']:
                raise ValueError('Recorded slot outside published port range')
            runtime = Runtime(context)
            manifest = context.manifest
            slot.update(number=k, urls={a: runtime.template(manifest.projects[a].server.url, a, number or 0, k)
                                        for a in manifest.servers()})
            if number is None:
                slot['urls'] = {a: f'http://localhost:{manifest.projects[a].server.owner_port}'
                                for a in manifest.servers() if manifest.projects[a].server.owner_port}
            warnings.append('Slot is recorded only; liveness and lease freshness were not checked.')
    except (OSError, ValueError):
        warnings.append('Slot evidence unreadable or ambiguous; run owner-side slots inspection.')
    logs = {}
    if number:
        logs['setup'] = str(context.lane_state(number) / 'setup.log')
        for alias in context.manifest.servers():
            project = context.manifest.projects[alias]
            logs[alias] = str(root / project.directory / project.server.log if project.server.log
                              else context.lane_state(number) / f'{alias}.log')
    next_step = ('Start subsequent shell commands with `' + command_prefix(root) + '`; use absolute lane paths for file tools.'
                 if number else 'Read-only work needs no attach. Before edits run bin/aw attach'
                 ' (--harness codex or --harness grok for those harnesses).')
    if warnings and kind == 'owner':
        next_step = 'Inspect attachment warnings before editing; do not allocate a second lane to resolve uncertainty.'
    return dict(schema_version=1, context=kind, lane=number, paths=paths, repos=repos,
                session=session, slot=slot, logs=logs, gate=context.gate_state(),
                next_step=next_step, warnings=warnings)
