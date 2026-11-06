"""Owner delegation: a time-limited record that lets owner-checkout agent sessions run
in-scope maintenance commands. Only the owner grants it, from a terminal; the hook refuses
`aw delegate` and edits of the record from tool calls. Lane sessions ignore it."""
import os
import re
import time

from .evidence import atomic

SCHEMA = 1
MIN_TTL, MAX_TTL = 5 * 60, 8 * 3600
# Scope name -> what it covers. Each entry is matched against the parsed command.
SCOPES = {
    'maintenance': 'bin/aw bootstrap, setup N, reconcile N --ended, park N, unpark N, '
                   'slots --reconcile, and any maintenance scripts named in workspace.toml',
}
MAINTENANCE_AGENT = {'bootstrap', 'setup', 'reconcile', 'park', 'unpark'}
# Owner-only commands no scope covers: starting agents or editors, deleting lanes,
# container lifecycle, scaffolding, applied sweeps and slot claims stay with the owner.
OWNER_ONLY_AGENT = MAINTENANCE_AGENT | {'new', 'resume', 'delete', 'open', 'containers', 'init'}


def path(context):
    return context.git_file('delegation.json')


def parse_ttl(text):
    match = re.fullmatch(r'([1-9][0-9]{0,3})([mh])', text or '')
    if not match:
        raise ValueError('TTL must look like 30m or 2h')
    seconds = int(match[1]) * (60 if match[2] == 'm' else 3600)
    if not MIN_TTL <= seconds <= MAX_TTL:
        raise ValueError('TTL must be between 5m and 8h')
    return seconds


def grant(context, scope, ttl, now=None):
    if context.caller:
        raise ValueError('Delegation is owner-only; lane sessions cannot grant or use it')
    if scope not in SCOPES:
        raise ValueError('Unknown delegation scope: ' + str(scope))
    now = time.time() if now is None else now
    record = dict(schema_version=SCHEMA, scope=scope, created_at=now, expires_at=now + parse_ttl(ttl),
                  uid=os.getuid())
    target = path(context)
    if target.is_symlink():
        raise ValueError('Refusing symlink delegation record')
    atomic(target, record)
    return record


def revoke(context):
    if context.caller:
        raise ValueError('Delegation is owner-only')
    target = path(context)
    if target.is_symlink():
        raise ValueError('Refusing symlink delegation record')
    target.unlink(missing_ok=True)


def current(context, now=None):
    """The valid delegation record, or None. Anything unexpected counts as no delegation."""
    if context.caller:
        return None
    target = path(context)
    try:
        if target.is_symlink() or not target.is_file() or target.stat().st_uid != os.getuid():
            return None
        import json
        record = json.loads(target.read_text())
    except (OSError, ValueError):
        return None
    now = time.time() if now is None else now
    try:
        valid = (record.get('schema_version') == SCHEMA and record.get('scope') in SCOPES
                 and record.get('uid') == os.getuid()
                 and record['created_at'] - 60 <= now < record['expires_at']
                 and 0 < record['expires_at'] - record['created_at'] <= MAX_TTL)
    except (KeyError, TypeError, AttributeError):
        return None
    return record if valid else None


def status(context, now=None):
    record = current(context, now)
    if not record:
        return dict(schema_version=1, delegated=False,
                    next='Owner grants from a terminal: bin/aw delegate --scope maintenance --ttl 1h')
    return dict(schema_version=1, delegated=True, scope=record['scope'], covers=SCOPES[record['scope']],
                expires_at=time.strftime('%Y-%m-%d %H:%M', time.localtime(record['expires_at'])))


WRAPPERS = {'bash', 'sh', 'exec', 'env', 'command', 'nohup', 'time', 'sudo', 'xargs', 'timeout'}


def segments(command):
    """Simple commands as word lists. Separators count only outside quotes, so text inside a
    quoted argument is never mistaken for a command. Unbalanced quoting falls back to a
    conservative split that treats every separator character as one."""
    out, words, word, quote, active, index = [], [], '', None, False, 0

    def end_word():
        nonlocal word, active
        if active:
            words.append(word)
        word, active = '', False

    def end_segment():
        nonlocal words
        end_word()
        if words:
            out.append(words)
        words = []

    while index < len(command):
        char = command[index]
        if quote == "'":
            if char == "'":
                quote = None
            else:
                word += char
        elif quote == '"':
            if char == '"':
                quote = None
            elif char == '\\' and index + 1 < len(command):
                index += 1
                word += command[index]
            else:
                word += char
        elif char in ('"', "'"):
            quote, active = char, True
        elif char == '\\' and index + 1 < len(command):
            index += 1
            word, active = word + command[index], True
        elif char == '#' and not active:
            while index < len(command) and command[index] != '\n':
                index += 1
            continue
        elif char in ';&|()\n`{}' or command.startswith('$(', index):
            end_segment()
            index += 2 if command.startswith('$(', index) else 1
            continue
        elif char.isspace():
            end_word()
        else:
            word, active = word + char, True
        index += 1
    if quote:
        return [part.split() for part in re.split(r'[;&|\n()`{}]+|\$\(', command) if part.split()]
    end_segment()
    return out


def agent_invocations(command, names=('aw',)):
    """(program, arguments) for each named bin/ program run in a shell command.

    A guardrail, not a shell parser: only the command word of each simple command (after
    assignments, flags and common wrappers) counts, so `git diff bin/bootstrap` does not.
    Command substitution inside double quotes is not inspected."""
    found = []
    for words in segments(command):
        for index, word in enumerate(words):
            if word in WRAPPERS or word.startswith('-') or re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*=.*', word) \
                    or re.fullmatch(r'[0-9]+[smh]?', word):
                continue
            parts = word.rstrip('/').split('/')
            if len(parts) >= 2 and parts[-2] == 'bin' and parts[-1] in names:
                found.append((parts[-1], words[index + 1:]))
            break
    return found


def owner_command(program, args, scripts=()):
    """Classify one invocation: (owner_only, scope that covers it or None)."""
    if program in scripts:
        return True, 'maintenance'
    if args[:1] == ['mine']:
        return True, None
    if args[:2] == ['slots', '--reconcile']:
        return True, 'maintenance'
    action = args[0] if args else ''
    if action == 'delegate':  # --status and --revoke never widen authority.
        return args[1:] not in (['--status'], ['--revoke']), None
    if action == 'sweep':
        return '--apply' in args, None
    if action in OWNER_ONLY_AGENT:
        return True, 'maintenance' if action in MAINTENANCE_AGENT else None
    return False, None


def decide(context, command):
    """Hook decision for an owner-checkout Bash command: None to allow, or a block reason."""
    record = None
    manifest = context.manifest
    scripts = manifest.maintenance_scripts
    for program, args in agent_invocations(command, (*context.tool_names, *scripts)):
        owner_only, scope = owner_command(program, args, scripts)
        if not owner_only:
            continue
        if args[:1] == ['delegate']:
            return ('Only the owner grants delegation, from a terminal: '
                    'bin/aw delegate --scope maintenance --ttl 1h')
        record = record or current(context)
        if scope and record and record['scope'] == scope:
            continue
        shown = ' '.join(['bin/' + program, *args])
        if scope:
            return (f'Owner-only command: {shown}. Ask the owner to run it, or to delegate '
                    f'{scope} from a terminal: bin/aw delegate --scope {scope} --ttl 1h')
        return f'Owner-only command outside every delegation scope: {shown}. Ask the owner to run it.'
    if path(context).name in command:
        return 'The delegation record is owner-managed; use bin/aw delegate --status'
    return None
