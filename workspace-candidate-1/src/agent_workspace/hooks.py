"""Harness adapters and confinement. Never waits on the maintenance gate."""
import json
from pathlib import Path
import re
import sys

from . import attachments, delegation
from .boundary import violation, within
from .context import Context
from .evidence import lock
from .identity import attach_command, command_prefix
from .payload import normalize
from .runtime import Runtime
from .state import health


def route(context, payload, at_start=False):
    session = payload.get('session_id', '')
    for harness in attachments.HARNESSES:
        path = attachments.path_for(context.state, harness, session)
        if not path.is_file() or path.is_symlink():
            continue
        with lock(path.with_suffix('.lock')):
            data = attachments.load(path)
            if not data:
                raise ValueError('Unreadable attachment record')
            data = attachments.refreshed(path, data)
            if data['state'] in attachments.PROTECTED:
                attachments.adopt(path, data)
            elif at_start:
                if not attachments.revive_locked(context.state, harness, session, context.lanes):
                    continue
                data = attachments.load(path)
            else:
                continue
            lane = context.validate_lane(data['lane'])
            generation = (context.lane_state(data['lane']) / 'generation').read_text().strip()
            if generation != data['generation']:
                raise ValueError('Stale attachment generation')
            return lane
    return None


def guard_command(command, context):
    if re.search(r'(^|[;&|(`\s])(pkill|killall|kill|fuser\s+.*-k)(\s|$)', command):
        return f'Do not kill shared processes; use {context.tool} stop for your servers.'
    if (re.search(r'\bdocker\s+((compose|container)\s+.*\b(down|stop|restart|rm|kill|up|create|run|start)\b|stop|rm|kill|restart|run|create|start|network\s+(rm|disconnect|create)|volume\s+rm|system\s+prune)\b', command)
            or re.search(r'\bdevcontainer\s+up\b|\bdocker\s+exec\b.*_devcontainer-', command)):
        return f'Containers are shared; use {context.tool} and owner-side maintenance commands.'
    manifest = context.manifest
    if any(re.search(pattern, command) for pattern in manifest.server_patterns):
        return f'Start servers with {context.tool} serve in the background.'
    for guard in manifest.guards:
        if re.search(guard.pattern, command):
            return guard.message
    return None


INACTIVE = ('Lane tooling is not yet activated in this checkout. '
            'Only read-only diagnosis is allowed: Read, Grep and Glob inside the workspace '
            '(not its lane or worktree directories), bin/aw whoami [--json], bin/aw doctor, '
            'git status and git log. Owner fix, from {main}: bin/aw bootstrap.')
# Any shell syntax rejects: allowed commands are fixed words, never parsed shell.
SHELL_META = set(';|&$`<>()\n\r\\\'"*?[]{}~!#=')
GIT_STATUS_FLAGS = {'-s', '--short', '-b', '--branch', '--porcelain', '-uall', '-uno'}
GIT_LOG_FLAGS = {'--oneline', '--graph', '--decorate', '--stat', '--all', '-n'}


def diagnostic_command(command, cwd, main, tools=('aw',)):
    """Exact read-only diagnostic forms, run from the workspace root."""
    if not command.strip() or any(c in SHELL_META for c in command) or cwd != main:
        return False
    tokens = command.split()
    if tokens[0] in {form for name in tools for form in ('bin/' + name, './bin/' + name, str(main / 'bin' / name))}:
        return tokens[1:] in (['whoami'], ['whoami', '--json'], ['doctor'])
    if tokens[:2] == ['git', 'status']:
        return set(tokens[2:]) <= GIT_STATUS_FLAGS
    if tokens[:2] == ['git', 'log']:
        return all(t in GIT_LOG_FLAGS or re.fullmatch(r'-?[1-9][0-9]{0,3}', t) for t in tokens[2:])
    return False


def diagnostic_read(payload, cwd, main, lanes):
    """Read, Grep and Glob inside the workspace but outside lanes and retained worktrees."""
    tool, args = payload.get('tool_name'), payload.get('tool_input') or {}
    paths = [args[k] for k in ('file_path', 'path') if args.get(k)]
    if tool in ('Grep', 'Glob') and not args.get('path'):
        paths.append(str(cwd))
    if tool == 'Glob':
        pattern = args.get('pattern', '')
        if pattern.startswith(('/', '~', '$')) or '..' in Path(pattern).parts:
            return False
    if not paths:
        return False
    for value in paths:
        if value.startswith(('~', '$')):
            return False
        path = (cwd / value).resolve()
        if not within(path, main) or within(path, lanes) or within(path, main / '.worktrees'):
            return False
    return True


def delegation_line(context):
    record = delegation.current(context)
    if not record:
        return None
    return (f"The owner delegated {record['scope']} to this checkout's sessions until "
            f"{delegation.status(context)['expires_at']}: you may run {delegation.SCOPES[record['scope']]}. "
            'Other owner-only commands still need the owner.')


def record_edit(payload, context):
    """True when a file tool targets the owner-managed delegation record."""
    args = payload.get('tool_input') or {}
    cwd = Path(payload.get('cwd') or context.root)
    for key in ('file_path', 'notebook_path', 'path'):
        if args.get(key) and (cwd / args[key]).resolve() == delegation.path(context).resolve():
            return True
    return False


def owner_checkout_guard(context, payload):
    """Unattached owner-checkout sessions: owner-only commands need a valid delegation."""
    tool = payload.get('tool_name')
    if tool in ('Edit', 'Write', 'MultiEdit', 'NotebookEdit') and record_edit(payload, context):
        return 'The delegation record is owner-managed; use bin/aw delegate --status'
    if tool == 'Bash':
        return delegation.decide(context, (payload.get('tool_input') or {}).get('command', ''))
    return None


def delegated_command(context, command, cwd, main):
    """Before activation, a delegated session may run one exact in-scope command from the root."""
    if not command.strip() or any(c in SHELL_META for c in command) or cwd != main:
        return False
    scripts = context.manifest.maintenance_scripts
    calls = delegation.agent_invocations(command, (*context.tool_names, *scripts))
    if len(calls) != 1 or command.split()[0] not in ('bin/' + calls[0][0], './bin/' + calls[0][0]):
        return False
    owner_only, scope = delegation.owner_command(*calls[0], scripts)
    return owner_only and scope is not None and delegation.decide(context, command) is None


def lane_owner_command(command, context):
    """Lane sessions never use delegation: owner-only commands stay owner-side."""
    scripts = context.manifest.maintenance_scripts
    for program, args in delegation.agent_invocations(command, (*context.tool_names, *scripts)):
        if delegation.owner_command(program, args, scripts)[0]:
            return 'Owner-only command; lane sessions cannot run it or use owner delegation.'
    return None


def degraded(context, mode, payload, state):
    """Guard for every state but active: diagnosis only, with one clear message."""
    message = INACTIVE.format(main=context.main)
    delegated = delegation_line(context)
    if mode == 'session-start':
        print(message)
        if delegated:
            print(delegated)
        return 0
    if mode == 'session-end':
        return 0
    if mode != 'pre-tool-use':
        raise ValueError('Unknown hook event')
    main = context.main.resolve()
    cwd = Path(payload.get('cwd') or context.root).resolve()
    tool, command = payload.get('tool_name'), (payload.get('tool_input') or {}).get('command', '')
    if not context.caller and (tool in ('Read', 'Grep', 'Glob') and diagnostic_read(payload, cwd, main, context.lanes)
                               or tool == 'Bash' and diagnostic_command(command, cwd, main, context.tool_names)
                               or tool == 'Bash' and delegated and delegated_command(context, command, cwd, main)):
        return 0
    print('Blocked: ' + message, file=sys.stderr)
    return 2


def run(context, mode, payload):
    attach = attach_command(payload)
    payload = normalize(payload)
    state = health(context)
    if state != 'active':
        return degraded(context, mode, payload, state)
    mapped = False
    if not context.caller:
        lane = route(context, payload, mode == 'session-start')
        if lane is None:
            if mode == 'session-start':
                print(f'You are in the owner checkout: {context.root}')
                print(f'Read-only work needs no attach. Before editing run {attach}.')
                print('Use the returned lane paths and command prefix; do not attach a second lane.')
                delegated = delegation_line(context)
                if delegated:
                    print(delegated)
            if mode == 'pre-tool-use':
                reason = owner_checkout_guard(context, payload)
                if reason:
                    print('Blocked: ' + reason, file=sys.stderr)
                    return 2
            return 0
        context = Context.discover(lane)
        mapped = True
    n = context.caller
    if mode == 'session-start':
        print(f'You are working in agent lane {n}: {context.root}')
        print(f'Work only in {context.root}. Start commands with `{command_prefix(context.root)}`; use absolute lane paths for edits.')
        if mapped:
            print('This attached conversation retains its original harness cwd; the command prefix enters the assigned lane.')
        print('Run project commands with bin/aw run <project>. Start servers with bin/aw serve in the background; return the slot with bin/aw stop.')
        print('Run bin/aw finish when handing off verified work; it preserves the lane and session reservation.')
        return 0
    if mode == 'pre-tool-use':
        reason = violation(payload, context.root, context.lane_state(n), context.main, mapped, context.tool_names)
        if not reason and payload.get('tool_name') == 'Bash':
            command = payload.get('tool_input', {}).get('command', '')
            reason = guard_command(command, context) or lane_owner_command(command, context)
        if reason:
            print('Blocked: ' + reason, file=sys.stderr)
            return 2
        return 0
    if mode == 'session-end':
        # Never end ownership from SessionEnd. Runtime stop is explicit and gated.
        with context.gate():
            Runtime(context).stop(n)
        return 0
    raise ValueError('Unknown hook event')


def main(root, mode, stream=None):
    """Entry for harness hooks. A guard failure must block the tool call, so it exits 2."""
    try:
        return run(Context.discover(root), mode, json.load(stream or sys.stdin))
    except (OSError, ValueError, KeyError, TypeError) as exc:
        print('Blocked: the lane guard could not run: ' + str(exc), file=sys.stderr)
        return 2 if mode == 'pre-tool-use' else 1
