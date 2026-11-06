"""The aw command line. Scaffolding, lane operations and project commands."""
import argparse
import json
import os
from pathlib import Path
import sys

from .context import Context, lane_id
from .evidence import lock
from .inspection import Inspection, recap, observe_sessions
from .operations import Operations
from .presentation import Terminal, emit_json, compact_sweep
from .runtime import Runtime, forward
from .state import protocol
from . import attachments, docs, hooks, scaffold
from .identity import whoami


def parser():
    p = argparse.ArgumentParser(prog='aw', description='Workspace recap, agent sessions and lane maintenance')
    subs = p.add_subparsers(dest='action')
    s = subs.add_parser('whoami', help='Observe caller lane, repos, recorded slot and log paths without changing state')
    s.add_argument('--json', action='store_true')
    for name in ('recap', 'ls', 'sweep'):
        s = subs.add_parser(name)
        s.add_argument('--json', action='store_true')
        s.add_argument('--verbose', action='store_true')
        s.add_argument('--remote', action='store_true')
        s.add_argument('--lane', type=lane_id)
        if name == 'recap':
            s.add_argument('--all', action='store_true')
        if name == 'sweep':
            s.add_argument('--explain', action='store_true')
            s.add_argument('--apply', action='store_true')
            s.add_argument('--retry')
    for name in ('new', 'resume'):
        s = subs.add_parser(name)
        if name == 'resume':
            s.add_argument('lane', type=lane_id)
        s.add_argument('--harness', choices=('claude', 'codex', 'grok'), default='claude')
    s = subs.add_parser('attach')
    s.add_argument('--harness', choices=('claude', 'codex', 'grok'), default='claude')
    s.add_argument('--json', action='store_true')
    for name in ('delete', 'park', 'unpark', 'setup', 'reconcile', 'attachments', 'open'):
        s = subs.add_parser(name)
        s.add_argument('lane', type=lane_id, nargs='?' if name == 'attachments' else None)
        if name == 'reconcile':
            s.add_argument('--ended', action='store_true', required=True)
        if name == 'open':
            s.add_argument('surface', nargs='?', help='a project alias, or host')
        if name == 'attachments':
            s.add_argument('--json', action='store_true')
    s = subs.add_parser('finish', help='Record committed work; default requires exact pushed heads and open MRs unless merged',
                        description='Record a handoff without releasing lane ownership. Default MR delivery requires clean repos, '
                        'exact pushed heads and open MRs unless independently merged. Local delivery needs explicit authorization '
                        'for owner review of local commits; it never supplies cleanup merge proof.')
    choice = s.add_mutually_exclusive_group(required=True)
    choice.add_argument('--plan')
    choice.add_argument('--no-plan', action='store_true')
    s.add_argument('--summary', required=True)
    s.add_argument('--delivery', choices=('mr', 'local'), default='mr', help='mr (default) or explicitly authorized local review')
    s.add_argument('--reason', help='Required nonempty one-line reason for local delivery only')
    s = subs.add_parser('delegate', help='Owner, from a terminal: let owner-checkout agent sessions run '
                        'in-scope maintenance commands until the TTL ends',
                        description='Grant (--scope maintenance --ttl 1h), inspect (--status) or end (--revoke) '
                        'delegation. Lane sessions never use it, and agent tool calls cannot grant it.')
    choice = s.add_mutually_exclusive_group(required=True)
    choice.add_argument('--scope', choices=('maintenance',))
    choice.add_argument('--status', action='store_true')
    choice.add_argument('--revoke', action='store_true')
    s.add_argument('--ttl', help='5m to 8h, for example 30m or 2h; required with --scope')
    s = subs.add_parser('containers')
    s.add_argument('operation', choices=('up', 'down', 'recreate'), default='up', nargs='?')
    s = subs.add_parser('doctor', help='Check this workspace and print what to fix; read-only')
    s.add_argument('--json', action='store_true')
    subs.add_parser('bootstrap', help='Clone missing projects, install git hooks and activate lanes')
    s = subs.add_parser('instructions', help='Regenerate the managed block in AGENTS.md')
    s.add_argument('--check', action='store_true')
    s = subs.add_parser('hooks-config', help='Regenerate harness hook configuration')
    s.add_argument('--check', action='store_true')
    s = subs.add_parser('plans', help='Show where plans stand')
    s.add_argument('slug', nargs='?')
    subs.add_parser('check-docs', help='Check plan and documentation conventions')
    s = subs.add_parser('plan-reminder', help='Remind when a plan listing this branch looks stale')
    s.add_argument('repo', nargs='?')
    return p


def agent(context, argv):
    forwarded = []
    if argv[:1] in (['new'], ['resume']) and '--' in argv:
        separator = argv.index('--')
        argv, forwarded = argv[:separator], argv[separator+1:]
    args = parser().parse_args(argv or ['recap'])
    args.arguments = forwarded
    terminal = Terminal(sys.stdout, title=context.manifest.name)
    if args.action == 'doctor':
        report = scaffold.doctor(context)
        emit_json(report, sys.stdout) if args.json else scaffold.print_doctor(report)
        return int(not report['ok'])
    if args.action == 'plans':
        return docs.plan_status(context, args.slug)
    if args.action == 'check-docs':
        return docs.check(context)
    if args.action == 'plan-reminder':
        return docs.plan_reminder(context, args.repo or os.getcwd())
    if args.action == 'instructions':
        return scaffold.write_instructions(context, args.check)
    if args.action == 'hooks-config':
        return scaffold.write_hooks(context.root, args.check)
    if args.action == 'bootstrap':
        return scaffold.bootstrap(context)
    if args.action == 'whoami':
        with context.gate(observe=True):
            report = whoami(context)
        if args.json:
            emit_json(report, sys.stdout)
        else:
            terminal.line(f"Context: {report['context']} · lane {report['lane'] or 'none'}")
            terminal.line('Workspace: ' + report['paths']['workspace'])
            for name, repo in report['repos'].items():
                terminal.line(f"{name}: {repo['branch'] or 'unknown/detached'} · {repo['head'] or 'unknown'} · dirty={repo['dirty']}")
            terminal.line('Slot: ' + str(report['slot']['number']) + ' (' + report['slot']['evidence'] + ')')
            terminal.line('Maintenance gate: ' + report['gate'])
            for name, url in (report['slot']['urls'] or {}).items():
                terminal.line(f'{name} URL: {url}')
            for name, path in report['logs'].items():
                terminal.line(f'{name} log: {path}')
            terminal.line(report['next_step'])
            for warning in report['warnings']:
                terminal.line('Warning: ' + warning)
        return 0
    if args.action in ('recap', 'ls') or args.action == 'sweep' and not args.apply:
        if args.action == 'sweep' and args.retry:
            raise ValueError('--retry requires sweep --apply')
        with context.gate(observe=True):
            if args.action == 'recap':
                report = recap(context, remote=args.remote, number=args.lane, include_all=args.all)
                emit_json(report, sys.stdout) if args.json else terminal.recap(report, args.verbose)
            else:
                entries = Inspection(context, args.remote).entries(args.lane)
                report = dict(entries=entries, errors=[])
                emit_json(compact_sweep(report, args.verbose), sys.stdout) if args.json else terminal.sweep(report, args.verbose or getattr(args, 'explain', False))
        return 0
    if args.action == 'delegate':
        # Works before activation: a fresh checkout is where delegation helps most.
        from . import delegation
        if args.status:
            emit_json(delegation.status(context), sys.stdout)
        elif args.revoke:
            delegation.revoke(context)
            emit_json(delegation.status(context), sys.stdout)
        else:
            if not args.ttl:
                raise ValueError('delegate --scope requires --ttl, for example --ttl 1h')
            delegation.grant(context, args.scope, args.ttl)
            emit_json(delegation.status(context), sys.stdout)
        return 0
    if args.action == 'attachments':
        with context.gate(observe=True):
            number = context.caller or args.lane
            if context.caller and args.lane not in (None, context.caller):
                raise ValueError('Only caller-lane attachment inspection is allowed here')
            sessions = observe_sessions(context.state, number)
            if args.json:
                emit_json(dict(schema_version=1, sessions=[{k: s.get(k) for k in
                          ('lane', 'state', 'harness', 'session', 'processes', 'generation')} for s in sessions]), sys.stdout)
            else:
                print('LANE\tSTATE\tHARNESS\tPROCESS\tSESSION')
                for s in sessions:
                    pids = ','.join(str(p['pid']) for p in attachments.bound(s)) or '-'
                    print(f"{s.get('lane')}\t{s['state']}\t{s.get('harness', '?')}\tpid={pids}\t{s.get('session', '?')}")
        return 0
    if args.action == 'sweep' and (args.lane is not None or args.explain):
        raise ValueError('Filtered apply is unsupported; inspect --lane N --explain, then use owner-side delete N')
    if protocol(context) != 2:
        raise ValueError('Lane tooling is not activated here; the owner runs bin/aw bootstrap first')
    owner_only = {'new', 'resume', 'delete', 'park', 'unpark', 'setup', 'reconcile', 'open', 'containers', 'sweep'}
    if context.caller and args.action in owner_only:
        raise ValueError('Run this management command owner-side')
    if args.action == 'attach':
        variable = {'claude': 'CLAUDE_CODE_SESSION_ID', 'codex': 'CODEX_SESSION_ID', 'grok': 'GROK_SESSION_ID'}[args.harness]
        session = os.environ.get(variable)
        if not session:
            raise ValueError(f'Not running inside a {args.harness.title()} session: no session identity available.')
        path = attachments.path_for(context.state, args.harness, session)
        path.parent.mkdir(parents=True, exist_ok=True)
        # Serialize same-session attaches before acquiring the nonblocking maintenance gate.
        with lock(path.with_suffix('.lock')):
            with context.gate():
                data = attachments.load(path)
                if data:
                    data = attachments.refreshed(path, data)
            # Existing membership needs only a shared gate; new allocation needs exclusive.
            existing = bool(data and data['state'] in attachments.PROTECTED)
            with context.gate(exclusive=not existing, timeout=2):
                ops = Operations(context)
                ops.runtime.initialize()
                emit_json(ops.attach(args.harness, serialized=True, allow_allocate=not existing), sys.stdout)
        return 0
    exclusive = args.action not in ('finish', 'open')
    with context.gate(exclusive=exclusive) as gate:
        ops = Operations(context)
        ops.runtime.initialize()
        if args.action == 'finish':
            if not context.caller:
                raise ValueError('Run finish in the assigned lane')
            record = ops.life.finish(context.caller, args.plan, args.summary, args.delivery, args.reason)
            print('Session handoff recorded. Review and effort verification may remain.')
            emit_json(record, sys.stdout)
        elif args.action == 'new':
            n = ops.allocate()
            return ops.launch(n, args.harness, strip_separator(args.arguments), gate)
        elif args.action == 'resume':
            return ops.resume(args.lane, args.harness, strip_separator(args.arguments), gate)
        elif args.action == 'delete':
            ops.delete(args.lane)
        elif args.action in ('park', 'unpark'):
            context.validate_lane(args.lane)
            path = context.lane_state(args.lane) / 'parked'
            path.touch() if args.action == 'park' else path.unlink(missing_ok=True)
        elif args.action == 'reconcile':
            for path, session in attachments.records(context.state):
                if session.get('lane') == args.lane:
                    session.update(state='Ended', ended_reason='owner reconciliation')
                    attachments.save(path, session)
        elif args.action == 'setup':
            ops.require_available(args.lane)
            ops.start_setup(args.lane)
        elif args.action == 'containers':
            ops.containers(args.operation)
        elif args.action == 'open':
            path = context.validate_lane(args.lane)
            surface = args.surface or next(iter(context.projects))
            project = context.manifest.projects.get(surface)
            if surface == 'host' or (project and project.runner == 'host'):
                return forward(['code', path / (project.directory if project else '.')])
            if not project:
                raise ValueError('open takes a project alias or host')
            container = json.dumps(dict(containerName='/' + ops.runtime.container(surface)), separators=(',', ':')).encode().hex()
            return forward(['code', '--folder-uri', f'vscode-remote://attached-container+{container}{path}/{project.directory}'])
        elif args.action == 'sweep':
            ops.reconcile_sessions()
            report = ops.life.retry(args.retry) if args.retry else ops.life.sweep(True)
            if args.retry:
                emit_json(report, sys.stdout)
            else:
                report['applied'] = True
                emit_json(compact_sweep(report, args.verbose), sys.stdout) if args.json else terminal.sweep(report, args.verbose)
                return int(bool(report['errors']))
    return 0


def strip_separator(args):
    return args[1:] if args[:1] == ['--'] else args


RUNTIME_VERBS = ('run', 'serve', 'stop', 'slots', 'mine', 'task', 'status')
RUNTIME_HELP = '''Usage: aw run <project> [<command...>|--up]
       aw serve [<project>...] [--no-wait]       (lane; run in background)
       aw stop                                   (lane; return your slot)
       aw slots [--json|--reconcile]             (recorded leases; reconciliation owner-only)
       aw mine [--done]                          (owner-only slot claim/return)
       aw task <name> [<arguments...>]           (lane; tasks from workspace.toml)
       aw status                                 (container observation)
Use aw whoami for lane, branches, recorded slot, URLs and logs.
Servers must come from aw serve in the background. Return the slot with aw stop.
Project commands are forwarded unchanged; no command opens a shell, --up ensures the container.
'''


def validate_runtime(context, argv):
    '''Validate public syntax before any gate, setup, timestamp or Docker work.'''
    cmd, args = argv[0], argv[1:]
    serve_words = {*context.projects, 'both', 'all', '--no-wait'}
    valid = (cmd == 'run' and args and args[0] in context.projects
             or cmd in ('stop', 'status') and not args
             or cmd == 'slots' and args in ([], ['--json'], ['--reconcile'])
             or cmd == 'mine' and args in ([], ['--done'])
             or cmd == 'serve' and all(a in serve_words for a in args)
             or cmd == 'task' and args and args[0] in context.manifest.tasks)
    if not valid:
        raise ValueError('Invalid runtime command or arguments.\n' + RUNTIME_HELP)
    if context.caller and (cmd == 'mine' or cmd == 'slots' and args == ['--reconcile']):
        raise ValueError('This operation is owner-only.\n' + RUNTIME_HELP)
    if not context.caller and cmd in ('serve', 'stop', 'task'):
        raise ValueError('Run this runtime command in a lane.\n' + RUNTIME_HELP)


def runtime_command(context, argv):
    validate_runtime(context, argv)
    runtime = Runtime(context)
    if argv == ['status']:
        return runtime.status()  # Docker-only observation needs no lane state or writable gate.
    if argv[0] == 'slots' and argv[1:] != ['--reconcile']:
        with context.gate(observe=True):
            leases, queue = runtime.leases(), runtime.queue()
            if argv[1:]:
                emit_json(dict(schema_version=1, leases=leases, queue=queue,
                               note='Recorded leases; no reaping or live probes performed.'), sys.stdout)
            else:
                terminal = Terminal(sys.stdout, title=context.manifest.name)
                terminal.line(f'{context.manifest.name} · Server slots (recorded)', '1;36')
                terminal.line(f"{len(leases)} recorded leases · limit {context.settings['slots']}; live owner servers unchecked")
                for k, lease in leases.items():
                    terminal.line(f"Slot {k}: {lease['holder']} · granted {lease.get('granted', '?')}")
                for index, row in enumerate(sorted(queue), 1):
                    terminal.line(f'Queue {index}: lane {row[2]}' + (' (eviction priority)' if row[0] == 0 else ''))
                terminal.line('Observational only; owner-side slots --reconcile checks and reaps stale leases.')
        return 0
    if protocol(context) != 2:
        raise ValueError('Lane tooling is not activated here; the owner runs bin/aw bootstrap first')
    with context.gate():
        runtime.initialize()
        cmd, args = argv[0], argv[1:]
        if cmd == 'mine':
            return runtime.mine(args == ['--done'])
        if cmd == 'slots':
            with lock(context.state / 'lock'):
                runtime.reconcile_locked()
            return 0
        if cmd == 'run' and not context.caller:
            return runtime.owner_execute(args[0], args[1:])
        n = context.caller
        if not n:
            raise ValueError('Run this runtime command in a lane')
        context.validate_lane(n)
        (context.lane_state(n) / 'last').touch()
        if cmd == 'stop':
            runtime.stop(n)
            return 0
        in_setup = os.environ.get('AW_IN_SETUP') == '1'
        if not in_setup:  # Setup itself runs project commands and holds the setup lock.
            runtime.setup_wait(n)
            runtime.ensure_containers()
        if cmd == 'serve':
            wanted = [a for a in args if a in context.projects]
            return runtime.serve(n, wanted or None, '--no-wait' not in args)
        if cmd == 'task':
            return runtime.task(n, args[0], args[1:])
        if args[1:] == ['--up']:
            return 0
        runtime.refuse_servers(args[1:])
        return runtime.execute(n, args[0], args[1:] or ['bash'], busy=not in_setup)


def discover_root():
    '''The nearest workspace at or above the current directory, preferring a lane marker.'''
    path = Path.cwd().resolve()
    for candidate in (path, *path.parents):
        if (candidate / 'workspace.toml').is_file():
            return candidate
    return None


def main(root=None, argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    try:
        if argv[:1] == ['init']:
            return scaffold.init_command(argv[1:])
        if argv[:1] in (['-V'], ['--version']):
            from . import __version__
            print(__version__)
            return 0
        if argv[:1] in (['help'],):
            argv = ['--help']
        if argv[:1] in (['--help'], ['-h']) or (argv[:1] and argv[0] in RUNTIME_VERBS and argv[1:] in (['--help'], ['-h'])):
            if argv[0] in RUNTIME_VERBS:
                print(RUNTIME_HELP, end='')
                return 0
            parser().print_help()
            print('\n' + RUNTIME_HELP, end='')
            return 0
        root = root or discover_root()
        if not root:
            raise ValueError('No workspace.toml here or above; run aw init')
        if argv[:1] == ['hook']:
            return hooks.main(root, argv[1] if len(argv) > 1 else '')
        context = Context.discover(root)
        if argv[:1] and argv[0] in RUNTIME_VERBS:
            return runtime_command(context, argv)
        return agent(context, argv)
    except (OSError, ValueError, KeyError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 130
