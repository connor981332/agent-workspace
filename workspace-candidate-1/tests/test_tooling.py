"""Python CLI/runtime contracts; uses only fixture repos and fake external tools."""
from contextlib import redirect_stdout, redirect_stderr
import fcntl
import errno
import io
import json
import os
from pathlib import Path
import signal
import pty
import hashlib
import shlex
import subprocess
import sys
import time
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'src'))
sys.path.insert(0, str(Path(__file__).resolve().parent))
from agent_workspace.context import Context
from agent_workspace.inspection import recap, observe_sessions, Inspection
from agent_workspace.presentation import Terminal, brief, clean, compact_sweep, status_label
from agent_workspace.runtime import Runtime, forward, write_text
from agent_workspace.operations import Operations
from agent_workspace.state import activate, protocol
from agent_workspace.cli import agent, main as cli_main, runtime_command as rt
from agent_workspace import attachments
from agent_workspace import lifecycle as candidate
from agent_workspace.hooks import run as hook
from agent_workspace.plans import read_plan
from agent_workspace.payload import normalize
import test_lanes as old


class PythonToolingTests(unittest.TestCase):
    init_repo = old.LaneTests.init_repo
    make_lane = old.LifecycleTests.make_lane
    snapshot = old.LifecycleTests.snapshot
    commit = old.LifecycleTests.commit

    def setUp(self):
        old.LifecycleTests.setUp(self)
        (self.main / ".git/aw-tooling-version").unlink()
        self.override = patch.dict(os.environ, {'AW_WORKSPACE_BASE': 'origin/main', 'AW_COUNT_OWNER_SERVERS': '0'})
        self.override.start()
        self.addCleanup(self.override.stop)
        self.context = Context.discover(self.main)
        self.runtime = Runtime(self.context)

    def plan(self, root, name='effort.md', state='FULLY IMPLEMENTED'):
        path = root / 'plans' / name
        path.parent.mkdir(exist_ok=True)
        path.write_text('# A useful effort\n\n## Status\n\n**State:** '+state+' · **Updated:** 2026-10-26\n'
                        '**Done:** Code merged.\n**In progress:** production verification.\n'
                        '**Next step:** Run the dry run.\n**Branches / MRs:** recorded\n')
        return path

    def test_codex_read_only_gate_names_the_writable_root_setting(self):
        with patch.dict(os.environ, {'CODEX_SESSION_ID': 'real-session'}), \
                patch('pathlib.Path.open', side_effect=OSError(errno.EROFS, 'Read-only file system')):
            with self.assertRaisesRegex(ValueError, 'sandbox_workspace_write.writable_roots') as error:
                with self.context.gate():
                    self.fail('Read-only gate opened')
        self.assertIn(str(self.main / '.git'), str(error.exception))

    def fresh_checkout(self):
        """Remove every trace of activation: no .git/aw-* files and no lane state root."""
        for path in (self.main / '.git').glob('aw-*'):
            path.unlink()
        import shutil
        shutil.rmtree(self.lanes)
        return Context.discover(self.main)

    def test_observers_tolerate_a_never_created_gate(self):
        context = self.fresh_checkout()
        with context.gate(observe=True) as handle:
            self.assertIsNone(handle)
        for argv in (['whoami', '--json'], ['whoami']):
            with redirect_stdout(io.StringIO()) as output, \
                    patch.object(Runtime, 'initialize', side_effect=AssertionError('initialize')):
                self.assertEqual(agent(context, argv), 0)
            text = output.getvalue()
            self.assertIn('not created yet', text, argv)
            if argv[-1] == '--json':
                self.assertEqual(json.loads(text)['gate'], 'not created yet')
        self.assertEqual(list((self.main / '.git').glob('aw-*')), [])
        self.assertFalse(self.lanes.exists())
        # Mutating callers still create the gate, and observers then report it.
        with context.gate():
            pass
        self.assertEqual(context.gate_state(), 'present')
        with context.gate(observe=True) as handle:
            self.assertIsNotNone(handle)

    def test_inactive_guard_allows_only_read_only_diagnosis(self):
        context = self.fresh_checkout()
        with redirect_stdout(io.StringIO()) as output:
            self.assertEqual(hook(context, 'session-start', dict(session_id='fresh', cwd=str(self.main))), 0)
        self.assertEqual(output.getvalue().count('not yet activated'), 1)
        self.assertNotIn('Legacy', output.getvalue())
        self.assertEqual(hook(context, 'session-end', dict(session_id='fresh')), 0)
        lanes, worktrees = self.main / '.lanes/1/file', self.main / '.worktrees/backup/file'
        cases = [
            ('Read', dict(file_path=str(self.main / 'file')), 0),
            ('Read', dict(file_path='bin/aw'), 0),
            ('Read', dict(file_path=str(lanes)), 2),
            ('Read', dict(file_path=str(worktrees)), 2),
            ('Read', dict(file_path='.lanes/../.lanes/1/file'), 2),
            ('Read', dict(file_path='/etc/passwd'), 2),
            ('Read', dict(file_path='~/.ssh/id_rsa'), 2),
            ('Grep', dict(pattern='x'), 0),
            ('Grep', dict(pattern='x', path=str(self.main / '.lanes')), 2),
            ('Glob', dict(pattern='**/*.py'), 0),
            ('Glob', dict(pattern='../**'), 2),
            ('Glob', dict(pattern='/etc/*'), 2),
            ('Bash', dict(command='bin/aw whoami'), 0),
            ('Bash', dict(command='bin/aw whoami --json'), 0),
            ('Bash', dict(command='./bin/aw doctor'), 0),
            # The fixture path has spaces: unquoted, bash would run its first word instead.
            ('Bash', dict(command=str(self.main / 'bin/aw') + ' doctor'), 2),
            ('Bash', dict(command='git status --short'), 0),
            ('Bash', dict(command='git log --oneline -5'), 0),
            ('Bash', dict(command='bin/aw whoami; rm x'), 2),
            ('Bash', dict(command='bin/aw whoami && rm x'), 2),
            ('Bash', dict(command='bin/aw whoami | sh'), 2),
            ('Bash', dict(command='bin/aw whoami $(rm x)'), 2),
            ('Bash', dict(command='bin/aw whoami\nrm x'), 2),
            ('Bash', dict(command='bin/aw whoami > x'), 2),
            ('Bash', dict(command='bin/aw bootstrap'), 2),
            ('Bash', dict(command='bin/aw attach'), 2),
            ('Bash', dict(command='git log --output=x'), 2),
            ('Bash', dict(command='git status -c core.pager=x'), 2),
            ('Bash', dict(command='ls'), 2),
            ('Edit', dict(file_path=str(self.main / 'file'), old_string='a', new_string='b'), 2),
            ('Write', dict(file_path=str(self.main / 'file'), content='x'), 2),
            ('NotebookEdit', dict(notebook_path=str(self.main / 'x.ipynb')), 2),
        ]
        for tool, tool_input, expected in cases:
            payload = dict(session_id='fresh', cwd=str(self.main), tool_name=tool, tool_input=tool_input)
            with redirect_stderr(io.StringIO()) as error:
                self.assertEqual(hook(context, 'pre-tool-use', payload), expected, (tool, tool_input))
            if expected:
                self.assertIn('not yet activated', error.getvalue())
                self.assertIn('bin/aw bootstrap', error.getvalue())
        # Diagnostic shell commands are workspace-root forms only.
        payload = dict(session_id='fresh', cwd=str(self.base), tool_name='Bash',
                       tool_input=dict(command='bin/aw whoami'))
        with redirect_stderr(io.StringIO()):
            self.assertEqual(hook(context, 'pre-tool-use', payload), 2)
        self.assertEqual(list((self.main / '.git').glob('aw-*')), [])

    def test_inactive_hook_wrapper_end_to_end_and_broken_import_exits_2(self):
        self.fresh_checkout()

        def wrapper(command):
            payload = json.dumps(dict(session_id='e2e', cwd=str(self.main), tool_name='Bash',
                                      tool_input=dict(command=command)))
            return subprocess.run(['python3', str(self.main / 'bin/aw'), 'hook', 'pre-tool-use'], input=payload,
                                  text=True, capture_output=True, cwd=self.main)

        self.assertEqual(wrapper('bin/aw whoami').returncode, 0)
        blocked = wrapper('touch x')
        self.assertEqual(blocked.returncode, 2)
        self.assertIn('not yet activated', blocked.stderr)
        import shutil
        copy = self.base / 'broken-tool'
        shutil.copytree(Path(__file__).resolve().parents[1] / 'src', copy / 'src')
        (copy / 'src/agent_workspace/payload.py').write_text('raise ImportError("synthetic breakage")\n')
        with patch.dict(os.environ, {'AW_TOOL_PATH': str(copy)}):
            broken = wrapper('bin/aw whoami')
        self.assertEqual(broken.returncode, 2)
        self.assertIn('synthetic breakage', broken.stderr)

    def attach_env(self, session='fresh-attach'):
        return patch.dict(os.environ, {'CLAUDE_CODE_SESSION_ID': session, 'CLAUDE_PID': str(os.getpid())})

    def test_fresh_checkout_bootstraps_then_attaches_without_manual_repair(self):
        self.fresh_checkout()
        # Project repos are already present (stubbed), so bootstrap clones nothing.
        tools = self.base / 'present-tools'
        tools.mkdir()
        for name in ('docker', 'glab'):
            (tools / name).write_text('#!/bin/sh\nexit 0\n')
            (tools / name).chmod(0o755)
        environment = {**os.environ, 'PATH': f"{tools}:{os.environ['PATH']}", 'GIT_CONFIG_GLOBAL': str(self.base / 'no-gitconfig')}
        result = subprocess.run(['python3', str(self.main / 'bin/aw'), 'bootstrap'], text=True, capture_output=True,
                                cwd=self.main, env=environment)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('project-api: already cloned', result.stdout)
        self.assertIn('Activated lanes', result.stdout)
        context = Context.discover(self.main)
        self.assertEqual(protocol(context), 2)
        rerun = subprocess.run(['python3', str(self.main / 'bin/aw'), 'bootstrap'], text=True, capture_output=True, cwd=self.main, env=environment)
        self.assertEqual(rerun.returncode, 0, rerun.stderr)
        self.assertNotIn('Activated lanes', rerun.stdout)
        # Attach straight away: no touch, mkdir or rmdir in between.
        with self.attach_env(), patch.object(Operations, 'start_setup') as setup, \
                redirect_stdout(io.StringIO()) as output, redirect_stderr(io.StringIO()):
            self.assertEqual(agent(context, ['attach']), 0)
        setup.assert_called_once_with(1)
        self.assertEqual(json.loads(output.getvalue())['lane'], 1)
        self.assertTrue((self.lanes / '1/.lane').is_file())

    def owner_bash(self, command, context=None):
        payload = dict(session_id='owner-session', cwd=str(self.main), tool_name='Bash',
                       tool_input=dict(command=command))
        with redirect_stderr(io.StringIO()) as error:
            code = hook(context or self.context, 'pre-tool-use', payload)
        return code, error.getvalue()

    def test_delegation_lets_owner_checkout_run_only_in_scope_maintenance(self):
        from agent_workspace import delegation
        (self.main / '.git/aw-tooling-version').write_text('2\n')
        commands = {
            'bin/aw bootstrap': True,
            'cd /somewhere && ./bin/aw setup 2': True,
            'bin/aw reconcile 2 --ended': True,
            'bin/aw slots --reconcile': True,
            'bin/bootstrap': True,
            'bin/aw delete 3': False,
            'bin/aw new': False,
            'bin/aw sweep --apply': False,
            'bin/aw containers down': False,
            'bin/aw mine': False,
        }
        harmless = ['bin/aw whoami', 'bin/aw doctor', 'bin/aw sweep', 'bin/aw run api npm test',
                    'git diff bin/bootstrap', 'cat bin/aw', 'bin/aw delegate --status', 'bin/aw delegate --revoke',
                    # Mentions inside quoted arguments are text, not commands.
                    'echo "then run bin/aw setup 2; done"', "sed -i 's|`bin/aw setup 2` (owner)|x|' plan.md",
                    "git commit -m 'Explain bin/aw delete 3 && bin/aw mine'", 'bin/aw whoami # bin/aw delete 3']
        for command in ('x=$(bin/aw delete 3)', 'echo `bin/aw delete 3`', 'true && bin/aw delete 3',
                        'true\nbin/aw delete 3', "bin/aw delete 3 'unbalanced"):
            self.assertEqual(self.owner_bash(command)[0], 2, command)
        for command in [*commands, *harmless]:
            code, error = self.owner_bash(command)
            self.assertEqual(code, 0 if command in harmless else 2, command)
        self.assertIn('bin/aw delegate --scope maintenance --ttl 1h', self.owner_bash('bin/aw setup 2')[1])
        # Granted: in-scope commands pass, out-of-scope owner commands stay blocked.
        with redirect_stdout(io.StringIO()) as output:
            self.assertEqual(agent(self.context, ['delegate', '--scope', 'maintenance', '--ttl', '1h']), 0)
        self.assertTrue(json.loads(output.getvalue())['delegated'])
        for command, delegable in commands.items():
            code, error = self.owner_bash(command)
            self.assertEqual(code, 0 if delegable else 2, command)
            if not delegable:
                self.assertIn('outside every delegation scope', error)
        with redirect_stdout(io.StringIO()) as output:
            hook(self.context, 'session-start', {'session_id': 'owner-session'})
        self.assertIn('delegated maintenance', output.getvalue())
        # Agents never grant or edit delegation, even while delegated.
        for command in ('bin/aw delegate --scope maintenance --ttl 8h', 'bash bin/aw delegate --scope maintenance --ttl 1h',
                        'echo {} > .git/aw-delegation.json'):
            self.assertEqual(self.owner_bash(command)[0], 2, command)
        edit = dict(session_id='owner-session', cwd=str(self.main), tool_name='Write',
                    tool_input=dict(file_path='.git/aw-delegation.json', content='{}'))
        with redirect_stderr(io.StringIO()):
            self.assertEqual(hook(self.context, 'pre-tool-use', edit), 2)
        # Lane sessions ignore delegation.
        lane = self.make_lane()
        local = Context.discover(lane)
        payload = dict(session_id='lane-session', cwd=str(lane), tool_name='Bash',
                       tool_input=dict(command=f'cd {shlex.quote(str(lane))} && bin/aw bootstrap'))
        with redirect_stderr(io.StringIO()) as error:
            self.assertEqual(hook(local, 'pre-tool-use', payload), 2)
        self.assertIn('lane sessions cannot', error.getvalue())
        self.assertIsNone(delegation.current(local))
        with self.assertRaisesRegex(ValueError, 'owner-only'):
            delegation.grant(local, 'maintenance', '1h')
        # Revoked, expired and tampered records are ignored.
        with redirect_stdout(io.StringIO()):
            agent(self.context, ['delegate', '--revoke'])
        self.assertEqual(self.owner_bash('bin/aw setup 2')[0], 2)
        delegation.grant(self.context, 'maintenance', '1h', now=time.time() - 2 * 3600)
        self.assertIsNone(delegation.current(self.context))
        self.assertEqual(self.owner_bash('bin/aw setup 2')[0], 2)
        record = delegation.grant(self.context, 'maintenance', '1h')
        record['expires_at'] = record['created_at'] + 30 * 24 * 3600
        delegation.path(self.context).write_text(json.dumps(record))
        self.assertIsNone(delegation.current(self.context))
        for ttl in ('4m', '9h', '1d', 'soon', ''):
            with self.assertRaisesRegex(ValueError, 'TTL'):
                delegation.parse_ttl(ttl)

    def test_delegation_before_activation_allows_exact_bootstrap_commands(self):
        from agent_workspace import delegation
        context = self.fresh_checkout()
        self.assertEqual(self.owner_bash('bin/aw bootstrap', context)[0], 2)
        delegation.grant(context, 'maintenance', '30m')
        for command, expected in (('bin/aw bootstrap', 0), ('bin/bootstrap', 0),
                                  ('./bin/aw bootstrap', 0), ('bin/aw bootstrap; rm x', 2),
                                  ('bin/aw delete 3', 2), ('bin/aw delegate --scope maintenance --ttl 1h', 2),
                                  ('touch x', 2)):
            self.assertEqual(self.owner_bash(command, context)[0], expected, command)
        with redirect_stdout(io.StringIO()) as output:
            hook(context, 'session-start', {'session_id': 'owner-session'})
        self.assertEqual(output.getvalue().count('not yet activated'), 1)
        self.assertIn('delegated maintenance', output.getvalue())

    def test_attach_without_project_repos_allocates_nothing(self):
        (self.main / '.git/aw-tooling-version').write_text('2\n')
        (self.main / 'project-next').rename(self.base / 'next-elsewhere')
        branches = old.run('git', 'branch', '--list', 'agent/*', cwd=self.main).stdout
        with self.attach_env(), patch.object(Operations, 'start_setup') as setup:
            with self.assertRaisesRegex(ValueError, 'project-next.*bin/aw bootstrap'):
                agent(self.context, ['attach'])
        setup.assert_not_called()
        self.assertFalse((self.lanes / '1').exists())
        self.assertEqual(list((self.state / 'lanes').glob('*')), [])
        self.assertEqual(old.run('git', 'branch', '--list', 'agent/*', cwd=self.main).stdout, branches)
        self.assertEqual(list((self.state / 'attachments').glob('*.json')), [])

    def test_identity_is_observational_and_handles_unknown_slot(self):
        from agent_workspace.identity import whoami
        path = self.make_lane()
        ctx = Context.discover(path)
        before = self.snapshot()
        with patch.object(Runtime, 'initialize', side_effect=AssertionError('initialization')), \
                patch('agent_workspace.attachments.refreshed', side_effect=AssertionError('refresh')):
            info = whoami(ctx)
        self.assertEqual(before, self.snapshot())
        self.assertEqual(info['lane'], 1)
        self.assertEqual(info['context'], 'lane')
        self.assertEqual(set(info['repos']), {'workspace', 'api', 'next'})
        self.assertIsNone(info['slot']['urls'])
        (self.state / 'slots/2').write_text('holder=lane1\ngranted=1\n')
        info = whoami(ctx)
        self.assertEqual(info['slot']['urls']['next'], 'http://localhost:4200')
        self.assertIn('unverified', info['slot']['evidence'])
        self.assertTrue(info['warnings'])
        (self.state / 'slots/3').write_text('holder=lane1\n')
        info = whoami(ctx)
        self.assertIsNone(info['slot']['number'])
        self.assertTrue(info['warnings'])
        self.assertNotIn('fixture-database-password', json.dumps(info))

    def test_owner_startup_guidance_for_all_harnesses_is_observational(self):
        (self.main / '.git/aw-tooling-version').write_text('2\n')
        keys = {'CLAUDE_CODE_SESSION_ID': '', 'CODEX_SESSION_ID': '', 'GROK_SESSION_ID': ''}
        for harness, key in (('claude', 'CLAUDE_CODE_SESSION_ID'), ('codex', 'CODEX_SESSION_ID'), ('grok', 'GROK_SESSION_ID')):
            payload = dict(session_id='startup', cwd=str(self.main))
            if harness == 'grok':
                payload = dict(hookEventName='SessionStart', sessionId='startup', cwd=str(self.main))
            before = self.snapshot()
            with patch.dict(os.environ, {**keys, key: 'startup'}), redirect_stdout(io.StringIO()) as output, \
                    patch.object(Runtime, 'initialize', side_effect=AssertionError('runtime')):
                self.assertEqual(hook(self.context, 'session-start', payload), 0)
            expected = 'bin/aw attach' + ('' if harness == 'claude' else ' --harness ' + harness)
            self.assertIn(expected, output.getvalue())
            self.assertIn('Read-only work needs no attach', output.getvalue())
            self.assertIn('owner checkout', output.getvalue())
            self.assertEqual(before, self.snapshot())

    def test_attached_startup_preserves_routing_and_generation_checks(self):
        path = self.make_lane()
        (self.main / '.git/aw-tooling-version').write_text('2\n')
        (self.state / 'lanes/1/generation').write_text('g')
        for harness in ('claude', 'grok', 'codex'):
            attachments.begin(self.state, harness, 'start-' + harness, 1, 'g', os.getpid())
            payload = dict(session_id='start-' + harness, cwd=str(self.main))
            if harness == 'grok':
                payload = dict(hookEventName='SessionStart', sessionId='start-grok', cwd=str(self.main))
            with patch.object(attachments, 'adopt'), redirect_stdout(io.StringIO()) as output:
                self.assertEqual(hook(self.context, 'session-start', payload), 0)
            self.assertIn('You are working in agent lane 1:', output.getvalue())
            self.assertIn(str(path), output.getvalue())
            self.assertIn('original harness cwd', output.getvalue())
        (self.state / 'lanes/1/generation').write_text('changed')
        with patch.object(attachments, 'adopt'), self.assertRaisesRegex(ValueError, 'Stale'):
            hook(self.context, 'session-start', dict(session_id='start-codex'))

    def test_identity_resolves_only_current_valid_attachment_without_writes(self):
        from agent_workspace.identity import whoami
        self.make_lane()
        generation = self.state / 'lanes/1/generation'
        generation.write_text('g')
        attachments.begin(self.state, 'claude', 'identity', 1, 'g', os.getpid())
        with patch.dict(os.environ, {'CLAUDE_CODE_SESSION_ID': 'identity'}):
            before = self.snapshot()
            info = whoami(self.context)
            self.assertEqual(info['context'], 'attached')
            self.assertEqual(info['lane'], 1)
            self.assertEqual(before, self.snapshot())
            generation.write_text('different')
            info = whoami(self.context)
            self.assertEqual(info['context'], 'owner')
            self.assertIsNone(info['lane'])
            self.assertIn('stale', str(info['warnings']))
            generation.write_text('g')
            attachments.begin(self.state, 'codex', 'other', 1, 'g', os.getpid())
            with patch.dict(os.environ, {'CODEX_SESSION_ID': 'other'}):
                info = whoami(self.context)
                self.assertIn('Ambiguous', str(info['warnings']))
                self.assertIsNone(info['lane'])
            p = attachments.path_for(self.state, 'claude', 'identity')
            data = attachments.load(p)
            data['state'] = 'Ended'
            attachments.save(p, data)
            self.assertIsNone(whoami(self.context)['lane'])

    def test_rt_usage_and_identity_do_not_initialize_or_acquire_writable_gate(self):
        for argv in (['run', '--help'], ['serve', '-h']):
            with patch.object(Context, 'gate', side_effect=AssertionError('gate')), \
                    patch.object(Runtime, 'initialize', side_effect=AssertionError('initialize')), \
                    redirect_stdout(io.StringIO()) as output:
                self.assertEqual(cli_main(self.main, argv), 0)
            self.assertIn('aw task', output.getvalue())
        for argv in (['unknown'], ['status', 'extra'], ['stop', 'extra'], ['task', 'oops'], ['serve', 'oops']):
            before = self.snapshot()
            with patch.object(Context, 'gate', side_effect=AssertionError('gate')), \
                    self.assertRaises(ValueError):
                rt(self.context, argv)
            self.assertEqual(before, self.snapshot())
        with patch.object(Runtime, 'owner_execute', return_value=0) as execute:
            (self.main / '.git/aw-tooling-version').write_text('2\n')
            rt(self.context, ['run', 'api', '--help'])
            execute.assert_called_once_with('api', ['--help'])
        with patch.object(Context, 'gate', wraps=self.context.gate) as gate, redirect_stdout(io.StringIO()):
            agent(self.context, ['whoami', '--json'])
            gate.assert_called_once_with(observe=True)

    def test_recovery_guidance_quotes_paths_and_never_replays_shell(self):
        from agent_workspace.boundary import violation
        from agent_workspace.identity import command_prefix
        import shlex
        lane = self.main / ".lanes/lane's space"
        command = 'cat ' + shlex.quote(str(self.main / 'file')) + ' secret-argument'
        reason = violation(dict(cwd=str(lane), tool_name='Bash', tool_input=dict(command=command)),
                           lane, self.state / 'lanes/1', self.main)
        self.assertIn(command_prefix(lane), reason)
        self.assertNotIn('secret-argument', reason)
        self.assertIn('absolute paths', reason)

    def handoff(self, n, plan=None):
        e = self.life.lane(n)
        record = dict(schema_version=1, lane=n, finished_at='2026-10-26T00:00:00Z', summary='Work handed off', plan=plan,
                      repos=[{k: r[k] for k in ('identity', 'head', 'branch', 'base_sha', 'base_ref', 'source')} for r in e['repos']])
        (self.state / f'lanes/{n}/handoff').write_text(json.dumps(record))
        return record

    def test_recap_is_observational_and_merging_does_not_complete_plan(self):
        path = self.make_lane()
        self.plan(path)
        self.plan(self.main)
        # Ignore the fixture plan file to keep Git's status separate from attention policy.
        for root in (path, self.main):
            old.run('git', 'add', 'plans/effort.md', cwd=root)
            old.run('git', 'commit', '-qm', 'plan', cwd=root)
        for rel, _ in self.bases.values():
            old.run('git', 'update-ref', 'refs/remotes/origin/main', 'HEAD', cwd=path / rel)
        self.handoff(1, 'plans/effort.md')
        before = self.snapshot()
        report = recap(self.context)
        effort = next(e for e in report['efforts'] if e['identity'] == 'plan:plans/effort.md')
        self.assertEqual(effort['attention'], 'Verify')
        self.assertEqual(effort['next_step'], 'Run the dry run.')
        self.assertEqual(before, self.snapshot())

    def test_scope_missing_plan_stale_handoff_and_untracked_work(self):
        path = self.make_lane()
        self.make_lane(2)
        self.handoff(1)
        self.commit(path)
        (path / 'valuable').write_text('keep me')
        local = Context.discover(path)
        report = recap(local)
        self.assertEqual(report['scope'], 'lane 1')
        self.assertTrue(all(e['lanes'] in ([1], []) for e in report['efforts']))
        self.assertIn('historical', str(report))
        self.assertEqual(report['efforts'][0]['attention'], 'Needs triage')
        with self.assertRaises(ValueError):
            recap(local, number=2)
        self.assertEqual((path / 'valuable').read_text(), 'keep me')

    def test_no_guessing_plan_association_and_json_is_compact(self):
        self.make_lane()
        self.plan(self.main)
        report = recap(self.context)
        self.assertEqual(len([e for e in report['efforts'] if e['lanes'] == [1]]), 1)
        self.assertTrue(any(e['source'].endswith('effort.md') and not e['lanes'] for e in report['efforts']))
        self.assertNotIn('base_sha', json.dumps(report))
        output = io.StringIO()
        Terminal(output, 40).recap(report)
        self.assertNotIn('\x1b', output.getvalue())
        self.assertTrue(all(len(line) <= 40 for line in output.getvalue().splitlines()))

    def test_recap_view_labels_groups_and_commands(self):
        def effort(title, attention, **kw):
            base = dict(identity=title, title=title, attention=attention, summary='s', next_step='n', lanes=[], session='unknown', quiet=False,
                        code='unknown', cleanup='x', source='git refs', resume=None, command=None, warnings=[], migration_changes=0)
            return dict(base, **kw)
        long = 'the owner runs a job; confirm the account is out of the sandbox; ' + 'then keep going with more words ' * 8
        report = dict(scope='workspace', collected_at='2026-10-29T14:49:00+00:00', warnings=[], efforts=[
            effort('Merged', 'In session', quiet=True, code='merged', next_step='No action required for merged tracked work.', lanes=[4]),
            effort('Owner plan', 'Your action', next_step=long),
            effort('lane work', 'Continue', next_step='Look at what changed, then resume the lane or discard the work.', lanes=[1],
                   command='bin/aw sweep --lane 1 --explain'),
            effort('docs/cheat', 'Needs triage', command='git -C . log --oneline -10 docs/cheat'),
            effort('cheat', 'Needs triage', command='git -C /w/cheat status --short')])
        self.assertEqual([status_label(e) for e in report['efforts']], ['Done', 'Your action', 'Continue', 'Needs triage', 'Needs triage'])
        output = io.StringIO()
        Terminal(output, 60).recap(report)
        text = output.getvalue()
        self.assertIn('Do this first: Owner plan', text)
        self.assertIn('Run     bin/aw sweep --lane 1 --explain', text)
        self.assertNotIn('WORK ', text)
        self.assertIn('NEEDS TRIAGE · 1 items', text)
        self.assertIn('• cheat + docs/cheat', text)
        self.assertIn('Done (1): Merged', text)
        self.assertEqual(text.count('--verbose'), 1)
        self.assertTrue(all(len(line) <= 60 for line in text.splitlines()))
        verbose = io.StringIO()
        Terminal(verbose, 60).recap(report, verbose=True)
        self.assertIn('DONE · Merged', verbose.getvalue())

    def test_brief_cuts_at_clause_boundaries(self):
        self.assertEqual(brief('Short.'), ('Short.', False))
        text, cut = brief('Do the first thing; then the second thing; ' + 'and a very long tail ' * 20)
        self.assertEqual((text, cut), ('Do the first thing; then the second thing …', True))
        text, cut = brief('word ' * 100)
        self.assertTrue(cut and len(text) <= 172 and text.endswith(' …'))

    def test_terminal_controls_and_no_color(self):
        self.assertEqual(clean('hello\x1b[31mred\x1b[0m\n\x07'), 'hellored')
        output = io.StringIO()
        output.isatty = lambda: True
        with patch.dict(os.environ, {'NO_COLOR': '1'}):
            Terminal(output).line('test', '31')
        self.assertEqual(output.getvalue(), 'test\n')

    def test_observe_sessions_does_not_refresh_record(self):
        self.make_lane()
        attachments.begin(self.state, 'claude', 'ended', 1, 'generation', 99999999)
        before = self.snapshot()
        self.assertEqual(observe_sessions(self.state)[0]['state'], 'Uncertain')
        self.assertEqual(before, self.snapshot())
        path = attachments.path_for(self.state, 'claude', 'ended')
        path.write_text('{bad')
        self.assertEqual(observe_sessions(self.state)[0]['state'], 'Uncertain')

    def test_plan_status_recognizes_linked_git_file(self):
        path = self.make_lane()
        result = old.run('python3', str(path / 'bin/aw'), 'plans')
        self.assertNotIn('not cloned', result.stdout)

    def test_default_cli_and_removed_commands_have_no_effects(self):
        self.make_lane()
        before = self.snapshot()
        output = io.StringIO()
        with redirect_stdout(output):
            self.assertEqual(agent(self.context, []), 0)
        self.assertIn('Welcome back', output.getvalue())
        self.assertEqual(before, self.snapshot())
        with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            agent(self.context, ['release', '1'])
        with self.assertRaises(ValueError):
            rt(self.context, ['release'])

    def test_explicit_harness_arguments_keep_boundaries(self):
        with patch('agent_workspace.cli.protocol', return_value=2), patch.object(Operations, 'allocate', return_value=1), patch.object(Operations, 'launch', return_value=0) as launch:
            agent(self.context, ['new', '--harness', 'codex', '--', '--model', 'a b', 'literal $value'])
        self.assertEqual(launch.call_args.args[1:3], ('codex', ['--model', 'a b', 'literal $value']))

    def test_rt_slots_never_reaps(self):
        (self.state / 'slots/1').write_text('holder=lane1\ngranted=1\n')
        before = self.snapshot()
        with redirect_stdout(io.StringIO()), patch.object(Runtime, 'reconcile_locked', side_effect=AssertionError('mutation')):
            rt(self.context, ['slots'])
        self.assertEqual(before, self.snapshot())

    def test_env_and_command_forwarding_exit_and_busy_cleanup(self):
        self.make_lane()
        (self.state / 'slots/1').write_text(f'holder=lane1\ngranted={int(time.time())}\n')
        with patch('agent_workspace.runtime.forward', return_value=17) as execute:
            self.assertEqual(self.runtime.execute(1, 'api', ['printf', 'a b', '$x']), 17)
        args = list(map(str, execute.call_args.args[0]))
        self.assertEqual(args[-3:], ['printf','a b','$x'])
        self.assertIn('OAUTH_PUBLIC_URL=http://localhost:4150', args)
        self.assertFalse(list((self.state / 'lanes/1').glob('busy.*')))
        with patch('agent_workspace.runtime.forward', side_effect=KeyboardInterrupt), self.assertRaises(KeyboardInterrupt):
            self.runtime.execute(1, 'next', ['a'])
        self.assertFalse(list((self.state / 'lanes/1').glob('busy.*')))

    def test_owner_container_identity_and_tty(self):
        result = subprocess.CompletedProcess([],0,'container-one\n','')
        with patch('agent_workspace.runtime.command', return_value=result), patch('agent_workspace.runtime.forward', return_value=23) as execute:
            self.assertEqual(self.runtime.owner_execute('api', ['printf','a b']), 23)
        self.assertIn('/aw-api', execute.call_args.args[0])
        self.assertEqual(execute.call_args.args[0][-2:], ['printf', 'a b'])

    def test_queue_grant_release_priority_and_cancellation(self):
        self.make_lane()
        self.make_lane(2)
        with patch.object(Runtime, 'reconcile_locked'), patch.object(Runtime, 'owner_running', return_value=False):
            self.context.settings['slots'] = 1
            self.assertEqual(self.runtime.try_grant(1), (1,0))
            self.assertEqual(self.runtime.try_grant(2), (None,1))
            with patch.object(Runtime, 'stop_server'):
                self.runtime.stop(1)
            self.assertEqual(self.runtime.try_grant(2), (1,0))
        with patch.object(Runtime, 'try_grant', return_value=(None,1)), patch('agent_workspace.runtime.time.sleep', side_effect=KeyboardInterrupt):
            with self.assertRaises(KeyboardInterrupt):
                self.runtime.serve(1)
        self.assertFalse(any(r[2] == 1 for r in self.runtime.queue()))

    def test_setup_failure_no_wait_and_server_refusal(self):
        self.make_lane()
        with self.assertRaisesRegex(ValueError, 'setup incomplete'):
            self.runtime.setup_wait(1)
        with patch.object(Runtime, 'try_grant', return_value=(None,1)):
            self.assertEqual(self.runtime.serve(1, wait=False), 3)
        with self.assertRaises(ValueError):
            self.runtime.refuse_servers(['npm','run','dev'])

    def test_start_failure_and_stale_pid_are_protected(self):
        self.make_lane()
        with patch.object(Runtime, 'try_grant', return_value=(1,0)), patch.object(Runtime, 'start_server', side_effect=ValueError('startup')):
            with self.assertRaisesRegex(ValueError, 'startup'):
                self.runtime.serve(1)
        self.assertFalse(list((self.state / 'lanes/1').glob('busy.*')))
        with patch.object(Runtime, 'server_identity', return_value=(100,'new-start',100)), patch('agent_workspace.runtime.checked') as execute:
            with self.assertRaisesRegex(ValueError, 'uncertain'):
                self.runtime.stop_server(1,'api')
            execute.assert_not_called()

    def test_unknown_config_and_symlinks_refused(self):
        manifest = self.main/'workspace.toml'
        manifest.write_text(manifest.read_text() + '\n[surprise]\nkey = 1\n')
        with self.assertRaisesRegex(ValueError, 'unknown keys'):
            Context.discover(self.main)
        self.plan(self.main)
        (self.main/'plans/escape.md').symlink_to(self.base/'outside')
        with self.assertRaises(ValueError):
            read_plan(self.main,'plans/escape.md')

    def test_v2_guard_without_maintenance_gate(self):
        path = self.make_lane()
        (self.main/'.git/aw-tooling-version').write_text('2\n')
        with self.context.gate(exclusive=True):
            payload = dict(cwd=str(path), tool_name='Read', tool_input=dict(file_path=str(self.main/'file')))
            self.assertEqual(hook(Context.discover(path), 'pre-tool-use', payload),2)
            payload['tool_input']['file_path'] = str(path/'file')
            self.assertEqual(hook(Context.discover(path), 'pre-tool-use', payload),0)

    def test_deduplication_owner_action_and_remote_budget(self):
        self.plan(self.main)
        text = (self.main/'plans/effort.md').read_text().replace('Run the dry run.', 'The owner runs the dry run.')
        (self.main/'plans/effort.md').write_text(text)
        old.run('git','add','plans/effort.md',cwd=self.main)
        old.run('git','commit','-qm','plan',cwd=self.main)
        old.run('git','update-ref','refs/remotes/origin/main','HEAD',cwd=self.main)
        for n in (1,2):
            self.make_lane(n)
            self.handoff(n,'plans/effort.md')
        report = recap(self.context)
        efforts = [e for e in report['efforts'] if e['identity'] == 'plan:plans/effort.md']
        self.assertEqual(len(efforts),1)
        self.assertEqual(efforts[0]['lanes'],[1,2])
        self.assertEqual(efforts[0]['attention'],'Your action')
        inspection = Inspection(self.context, True)
        inspection.remote_deadline = 0
        source = dict(host='gitlab.example',project='a/b',remote='origin',target='main')
        with patch('agent_workspace.forge.subprocess.run', side_effect=AssertionError('over budget')):
            self.assertIn('unknown', inspection.mr(source, 'branch', 'a'*40)['availability'])

    def test_owner_eviction_waits_for_busy_then_notifies_and_prioritizes(self):
        self.make_lane()
        self.context.settings['slots'] = 1
        self.context.settings['evict_grace_seconds'] = 10
        (self.state/'slots/1').write_text('holder=lane1\ngranted=1\n')
        with patch.object(Runtime,'reconcile_locked'), patch.object(Runtime,'busy',side_effect=[True,False]), \
                patch.object(Runtime,'stop_server'), patch('agent_workspace.runtime.time.sleep') as pause:
            self.assertEqual(self.runtime.mine(),0)
            pause.assert_called_once_with(3)
        self.assertEqual(self.runtime.leases()[1]['holder'],'owner')
        self.assertTrue((self.state/'lanes/1/priority').exists())
        self.assertIn('owner', (self.state/'lanes/1/notice').read_text())

    def test_report_size_and_latency_baseline(self):
        for n in range(1,7):
            self.make_lane(n)
        started = time.monotonic()
        baseline = self.life.sweep()
        old_time = time.monotonic()-started
        started = time.monotonic()
        report = recap(self.context, now=1793000000)
        new_time = time.monotonic()-started
        rendered = io.StringIO()
        Terminal(rendered,90).recap(report)
        self.assertLess(len(rendered.getvalue()),len(json.dumps(baseline,indent=2)))
        print(f'\nSix-lane fixture: sweep evidence {len(json.dumps(baseline,indent=2))} bytes/{old_time:.3f}s; recap text {len(rendered.getvalue())} bytes, JSON {len(json.dumps(report))} bytes/{new_time:.3f}s')

    def runner(self, body):
        path = self.base/'runner.py'
        library = Path(__file__).resolve().parents[1]/'src'
        path.write_text('import sys\nsys.path.insert(0,'+repr(str(library))+')\n'+body)
        return path

    def recorded_update(self):
        path = self.make_lane()
        name = 'bin/rt'
        original = (path/name).read_bytes()
        updated = original + b'\n# exact recorded tooling update\n'
        (path/name).write_bytes(updated)
        backup = self.main/'.worktrees/tooling-update-fixture'
        (backup/'bin').mkdir(parents=True)
        (backup/name).write_bytes(original)
        manifest = dict(lane=1,head=old.run('git','rev-parse','HEAD',cwd=path).stdout.strip(),
                        files=[dict(path=name,existed=True,sha256=hashlib.sha256(updated).hexdigest())])
        (backup/'manifest.json').write_text(json.dumps(manifest))
        (self.main/'.git/aw-tooling-cutover.json').write_text(json.dumps(dict(schema_version=1,status='active',
              source=old.run('git','rev-parse','HEAD',cwd=self.main).stdout.strip(),backups=[])))
        return path

    def test_real_forwarded_stdin_arguments_exit_and_signal(self):
        child = 'import sys,json; print(json.dumps([sys.argv[1:],sys.stdin.read()])); sys.exit(7)'
        path = self.runner('from agent_workspace.runtime import forward\nsys.exit(forward([sys.executable,"-c",'+repr(child)+',"a b","$literal"]))\n')
        result = subprocess.run([sys.executable,str(path)],input='payload\n',text=True,capture_output=True,timeout=10)
        self.assertEqual(result.returncode,7)
        self.assertEqual(json.loads(result.stdout),[['a b','$literal'],'payload\n'])
        ready = self.base/'child-ready'
        child = f'import os,time; open({str(ready)!r},"w").write(str(os.getpid())); time.sleep(60)'
        for sig, expected in ((signal.SIGTERM,143),(signal.SIGINT,130)):
            ready.unlink(missing_ok=True)
            path = self.runner('from agent_workspace.runtime import forward\nsys.exit(forward([sys.executable,"-c",'+repr(child)+']))\n')
            process = subprocess.Popen([sys.executable,str(path)],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            self.addCleanup(old.close_process,process)
            deadline = time.monotonic()+5
            while not ready.exists() and time.monotonic()<deadline:
                time.sleep(.02)
            self.assertTrue(ready.exists())
            pid = int(ready.read_text())
            process.send_signal(sig)
            process.communicate(timeout=10)
            self.assertEqual(process.returncode,expected)
            with self.assertRaises(ProcessLookupError):
                os.kill(pid,0)

    def test_real_pty_selects_interactive_container_flags(self):
        self.make_lane()
        directory = self.base/'tty-bin'
        directory.mkdir()
        captured = self.base/'argv.json'
        executable = directory/'docker'
        executable.write_text('#!'+sys.executable+'\nimport json,sys\nopen('+repr(str(captured))+',"w").write(json.dumps(sys.argv[1:]))\n')
        executable.chmod(0o755)
        path = self.runner('from agent_workspace.context import Context\nfrom agent_workspace.runtime import Runtime\nsys.exit(Runtime(Context.discover('+repr(str(self.main))+')).execute(1,"api",["printf","a b"]))\n')
        master, slave = pty.openpty()
        try:
            process = subprocess.Popen([sys.executable,str(path)],stdin=slave,stdout=slave,stderr=subprocess.PIPE,
                                       env={**os.environ,'PATH':str(directory)+':'+os.environ['PATH']})
            self.addCleanup(old.close_process,process)
            _, err = process.communicate(timeout=10)
            self.assertEqual(process.returncode,0,err)
            args = json.loads(captured.read_text())
            self.assertEqual(args[:2],['exec','-it'])
            self.assertEqual(args[-2:],['printf','a b'])
        finally:
            os.close(master)
            os.close(slave)


if __name__ == '__main__':
    unittest.main(verbosity=2)
