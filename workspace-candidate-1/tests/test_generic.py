"""The generalized surface: manifest, forges, launcher, onboarding and a real host-runner lane."""
import json
import os
from pathlib import Path
import socket
import stat
import subprocess
import sys
import tempfile
import time
import tomllib
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'src'))
sys.path.insert(0, str(ROOT / 'tests'))
from agent_workspace import forge, launcher, manifest, procinfo, scaffold
from agent_workspace.context import Context
from agent_workspace.evidence import remote_identity
import fixtures


def git(*args, cwd):
    return subprocess.run(['git', *args], cwd=cwd, check=True, capture_output=True, text=True).stdout.strip()


def free_port():
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        return probe.getsockname()[1]


class ManifestTests(unittest.TestCase):
    base = {'projects': {'app': {'directory': '.'}}}

    def test_defaults_and_fixture(self):
        parsed = manifest.parse(self.base)
        self.assertEqual((parsed.namespace, parsed.forge, parsed.env_prefix), ('aw', 'auto', 'AW_'))
        self.assertFalse(parsed.projects['app'].nested)
        full = manifest.parse(tomllib.loads(fixtures.MANIFEST))
        self.assertEqual(full.servers(), ['api', 'next'])
        self.assertEqual(full.servers(['next']), ['api', 'next'])
        self.assertEqual(full.servers(['api']), ['api'])

    def test_mistakes_are_errors_rather_than_ignored(self):
        cases = [
            ({'projects': {}}, 'At least one'),
            ({**self.base, 'typo': {}}, 'unknown keys'),
            ({**self.base, 'lanes': {'slot': 2}}, 'unknown keys'),
            ({**self.base, 'workspace': {'namespace': 'Bad-Name'}}, 'namespace'),
            ({**self.base, 'workspace': {'forge': 'bitbucket'}}, 'forge'),
            ({'projects': {'all': {'directory': 'x'}}}, 'alias'),
            ({'projects': {'a': {'directory': '../x'}}}, 'directory'),
            ({'projects': {'a': {'directory': '.'}, 'b': {'directory': 'b'}}}, 'only project'),
            ({'projects': {'a': {'directory': 'x'}, 'b': {'directory': 'x'}}}, 'unique'),
            ({'projects': {'a': {'runner': 'podman'}}}, 'runner'),
            ({'projects': {'a': {'runner': 'docker'}}}, 'container and service'),
            ({'projects': {'a': {'server': {'command': 'x'}}}}, 'base port'),
            ({'projects': {'a': {'server': {'command': 'x', 'port': 1, 'after': ['b']}}}}, 'no server named'),
            ({**self.base, 'guards': {'server_patterns': ['(']}}, ''),
            ({**self.base, 'tasks': {'t': {'command': 'x', 'quiesce': ['app']}}}, 'no server named'),
        ]
        for data, text in cases:
            with self.subTest(data=data), self.assertRaisesRegex(Exception, text):
                manifest.parse(data)

    def test_dependency_cycles_are_refused(self):
        server = lambda other: {'command': 'x', 'port': 1, 'after': [other]}
        parsed = manifest.parse({'projects': {'a': {'server': server('b')}, 'b': {'server': server('a')}}})
        with self.assertRaisesRegex(ValueError, 'cycle'):
            parsed.servers()


class FakeGitHub(forge.GitHub):
    def __init__(self, pages):
        super().__init__(lambda: 5)
        self.pages = pages

    def api(self, identity, endpoint):
        return self.pages


class ForgeTests(unittest.TestCase):
    source = {'host': 'github.com', 'project': 'o/r', 'remote': 'origin', 'target': 'main'}
    head = 'a' * 40

    def pull(self, **changes):
        repo = {'full_name': 'o/r'}
        pull = {'number': 7, 'state': 'closed', 'merged_at': '2026-01-01T00:00:00Z', 'html_url': 'u', 'draft': False,
                'head': {'ref': 'b', 'sha': self.head, 'repo': repo}, 'base': {'ref': 'main', 'repo': repo}}
        return {**pull, **changes}

    def change(self, *pulls):
        return FakeGitHub(list(pulls)).change(self.source, 'b', self.head)

    def test_exact_states(self):
        self.assertEqual(self.change(self.pull())['state'], 'merged')
        self.assertEqual(self.change(self.pull(state='open', merged_at=None))['state'], 'opened')
        self.assertEqual(self.change(self.pull(merged_at=None))['state'], 'closed')

    def test_everything_short_of_one_exact_match_is_not_merged(self):
        moved = self.pull(head={'ref': 'b', 'sha': 'b' * 40, 'repo': {'full_name': 'o/r'}})
        fork = self.pull(head={'ref': 'b', 'sha': self.head, 'repo': {'full_name': 'attacker/r'}})
        other_base = self.pull(base={'ref': 'develop', 'repo': {'full_name': 'o/r'}})
        deleted_repo = self.pull(head={'ref': 'b', 'sha': self.head, 'repo': None})
        self.assertEqual(self.change(moved)['availability'], 'source head mismatch')
        self.assertEqual(self.change(fork)['availability'], 'source head mismatch')
        self.assertEqual(self.change(deleted_repo)['availability'], 'source head mismatch')
        self.assertEqual(self.change(other_base)['availability'], 'confirmed absent')
        self.assertEqual(self.change()['availability'], 'confirmed absent')
        self.assertEqual(self.change(self.pull(), self.pull(number=8))['availability'], 'ambiguous')
        for result in (self.change(moved), self.change(fork), self.change(self.pull(), self.pull(number=8))):
            self.assertNotIn('state', result)

    def test_unavailable_api_and_pagination_limit_stay_unknown(self):
        self.assertIn('unknown', FakeGitHub(None).change(self.source, 'b', self.head)['availability'])
        self.assertIn('unknown', FakeGitHub([self.pull()] * 100).change(self.source, 'b', self.head)['availability'])

    def test_adapter_choice(self):
        left = lambda: 1
        chosen = lambda setting, host: forge.for_identity(setting, {'host': host, 'project': 'o/r'}, left)
        self.assertIsInstance(chosen('auto', 'github.com'), forge.GitHub)
        self.assertIsInstance(chosen('auto', 'gitlab.example.com'), forge.GitLab)
        self.assertIsNone(chosen('auto', 'code.example.com'))
        self.assertIsNone(chosen('none', 'github.com'))
        self.assertIsInstance(chosen('github', 'code.example.com'), forge.GitHub)
        self.assertEqual(remote_identity('git@github.com:o/r.git')['project'], 'o/r')


class ProcessIdentityTests(unittest.TestCase):
    def test_own_process(self):
        self.assertTrue(procinfo.start_identity(os.getpid()))
        self.assertEqual(procinfo.parent(os.getpid()), os.getppid())
        self.assertTrue(procinfo.names(os.getpid()))
        if not Path('/proc/self/stat').exists():  # The ps path, as on macOS. BusyBox ps lacks lstart.
            self.assertTrue(procinfo._ps(os.getpid(), 'lstart'))

    def test_dead_process_has_no_identity(self):
        child = subprocess.Popen(['true'])
        child.wait()
        self.assertIsNone(procinfo.start_identity(child.pid))


class LauncherTests(unittest.TestCase):
    def test_mutable_refs_are_refused(self):
        with self.assertRaisesRegex(ValueError, 'full commit SHA'):
            launcher.cached('https://example.invalid/x.git', 'main', '')

    def test_broken_tool_blocks_with_exit_2(self):
        with tempfile.TemporaryDirectory() as directory:
            scaffold.install_launcher(directory)
            aw = Path(directory) / 'bin/aw'
            self.assertTrue(aw.stat().st_mode & stat.S_IXUSR)
            (Path(directory) / 'workspace.toml').write_text('[workspace]\n')
            environment = {k: v for k, v in os.environ.items() if k != 'AW_TOOL_PATH'}
            self.assertEqual(subprocess.run([sys.executable, aw, 'hook', 'pre-tool-use'], env=environment, capture_output=True).returncode, 2)
            (Path(directory) / 'workspace.toml').write_text('not [valid')
            self.assertEqual(subprocess.run([sys.executable, aw, 'hook', 'pre-tool-use'], env=environment, capture_output=True).returncode, 2)
            self.assertEqual(subprocess.run([sys.executable, aw, 'whoami'], env={**environment, 'AW_TOOL_PATH': directory},
                                            capture_output=True).returncode, 2)

    def test_pin_fetches_that_commit_and_runs_it(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            tool = base / 'tool'
            package = tool / 'candidate/src/agent_workspace'
            package.mkdir(parents=True)
            (package / '__init__.py').write_text('')
            (package / 'cli.py').write_text('def main(root, argv):\n    print("pinned", root.name, argv)\n    return 0\n')
            git('init', '-q', cwd=tool)
            git('config', 'user.email', 'f@example.invalid', cwd=tool)
            git('config', 'user.name', 'F', cwd=tool)
            git('add', '.', cwd=tool)
            git('commit', '-qm', 'one', cwd=tool)
            ref = git('rev-parse', 'HEAD', cwd=tool)
            (package / 'cli.py').write_text('def main(root, argv):\n    print("moved")\n    return 0\n')
            git('commit', '-qam', 'two', cwd=tool)
            workspace = base / 'workspace'
            scaffold.install_launcher(workspace.mkdir() or workspace)
            (workspace / 'workspace.toml').write_text(
                f'[tool]\nsource = "file://{tool}"\nref = "{ref}"\nsubdir = "candidate"\n')
            environment = {**{k: v for k, v in os.environ.items() if k != 'AW_TOOL_PATH'}, 'XDG_CACHE_HOME': str(base / 'cache')}
            for _ in range(2):
                result = subprocess.run([sys.executable, workspace / 'bin/aw', 'x'], env=environment, capture_output=True, text=True)
                self.assertEqual((result.returncode, result.stdout.strip()), (0, "pinned workspace ['x']"), result.stderr)


MANIFEST = '''
[workspace]
name = "Solo"
namespace = "solo"
forge = "none"

[lanes]
workspace_base = "main"
project_base = "main"
count_owner_servers = 0

[lifecycle]
prepare = "scripts/prepare"
setup = ["scripts/setup", "--flag"]
teardown = "scripts/teardown"

[tasks.greet]
command = "scripts/greet"
description = "Print a greeting"

[projects.app]
directory = "."
install = ["sh", "-c", "echo installed >> install.count"]
install_stamp = "stamp.txt"
generated = ["install.count"]

[projects.app.server]
command = "exec python3 -m http.server $PORT --bind 127.0.0.1"
port = %d
url = "http://localhost:{port}/"

[projects.app.server.env]
PORT = "{port}"
GREETING = "lane {lane} slot {slot}"
'''

SCRIPT = '#!/bin/sh\nset -eu\n'


class HostLaneTests(unittest.TestCase):
    """No fakes: real Git, real processes, a real server and the real launcher. Docker is unused."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name).resolve()
        self.main = self.base / 'solo app'
        self.main.mkdir()
        self.sleeper = subprocess.Popen(['sleep', '300'])
        self.addCleanup(self.sleeper.wait)
        self.addCleanup(self.sleeper.kill)
        self.log = self.base / 'events'
        self.env = {**os.environ, 'AW_TOOL_PATH': str(ROOT), 'CLAUDE_CODE_SESSION_ID': 'solo-session',
                    'CLAUDE_PID': str(self.sleeper.pid), 'EVENT_LOG': str(self.log)}
        git('init', '-q', '-b', 'main', cwd=self.main)
        git('config', 'user.email', 'f@example.invalid', cwd=self.main)
        git('config', 'user.name', 'F', cwd=self.main)
        (self.main / 'stamp.txt').write_text('v1\n')
        (self.main / 'README.md').write_text('# Solo\n')
        git('add', '.', cwd=self.main)
        git('commit', '-qm', 'app', cwd=self.main)

    def aw(self, *args, cwd=None, ok=True, script=None):
        result = subprocess.run([sys.executable, script or self.main / 'bin/aw', *args], cwd=cwd or self.main,
                                env=self.env, text=True, capture_output=True)
        if ok and result.returncode:
            raise AssertionError(f'{args}: {result.stdout}{result.stderr}')
        return result

    def initialize(self):
        init = subprocess.run([sys.executable, '-c', 'import sys; sys.path.insert(0, sys.argv[1]); '
                               'from agent_workspace.cli import main; sys.exit(main(None, ["init", "--namespace", "solo"]))',
                               str(ROOT / 'src')], cwd=self.main, env=self.env, text=True, capture_output=True)
        self.assertEqual(init.returncode, 0, init.stderr)
        (self.main / 'workspace.toml').write_text(MANIFEST % free_port())
        scripts = self.main / 'scripts'
        scripts.mkdir()
        bodies = {
            'prepare': 'echo "prepare lane=$AW_LANE fresh=$AW_FRESH" >> "$EVENT_LOG"\n'
                       'printf "MARKER=from-env-file\\nSOLO_LANE=%s\\n" "$AW_LANE" > "$AW_LANE_STATE/app.env"\n',
            'setup': 'echo "setup args=$* in=$(basename "$PWD")" >> "$EVENT_LOG"\n'
                     '"$AW_TOOL" run app sh -c \'echo "run-in-setup $MARKER" >> "$EVENT_LOG"\'\n',
            'teardown': 'echo "teardown lane=$AW_LANE" >> "$EVENT_LOG"\n',
            'greet': 'echo "hello $1 from lane $AW_LANE"\n',
        }
        for name, body in bodies.items():
            (scripts / name).write_text(SCRIPT + body)
            (scripts / name).chmod(0o755)
        (self.main / '.gitignore').write_text((self.main / '.gitignore').read_text() + 'install.count\n')
        self.aw('instructions')
        git('add', '.', cwd=self.main)
        git('commit', '-qm', 'Add Agent Workspace', cwd=self.main)
        bootstrapped = self.aw('bootstrap')
        self.assertIn('All checks passed', bootstrapped.stdout)

    def wait_setup(self, n):
        path = self.main / f'.lanes/.solo/lanes/{n}'
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline and not (path / 'setup.ok').exists():
            time.sleep(.1)
        self.assertTrue((path / 'setup.ok').exists(), (path / 'setup.log').read_text())

    def test_single_repo_lane_from_init_to_cleanup(self):
        self.initialize()
        self.assertIn('Agent lanes', (self.main / 'AGENTS.md').read_text())
        info = json.loads(self.aw('attach').stdout)
        self.assertEqual(info['lane'], 1)
        self.assertEqual(set(info['paths']), {'workspace'})
        lane = self.main / '.lanes/1'
        self.assertEqual((lane / '.lane').read_text(), 'SOLO_LANE=1\n')
        self.wait_setup(1)
        events = self.log.read_text()
        self.assertIn('prepare lane=1 fresh=1', events)
        self.assertIn('setup args=--flag in=1', events)
        self.assertIn('run-in-setup from-env-file', events)
        self.assertEqual((lane / 'install.count').read_text(), 'installed\n')

        aw = lane / 'bin/aw'
        result = self.aw('run', 'app', 'sh', '-c', 'echo "$MARKER $SOLO_LANE $(basename "$PWD")"', cwd=lane, script=aw)
        self.assertEqual(result.stdout.strip(), 'from-env-file 1 1')
        self.assertEqual(self.aw('task', 'greet', 'world', cwd=lane, script=aw).stdout.strip(), 'hello world from lane 1')
        self.assertNotEqual(self.aw('task', 'missing', cwd=lane, script=aw, ok=False).returncode, 0)

        serving = self.aw('serve', cwd=lane, script=aw)
        port = tomllib.loads((self.main / 'workspace.toml').read_text())['projects']['app']['server']['port'] + 100
        self.assertIn(f'app: http://localhost:{port}/', serving.stdout)
        with urllib.request.urlopen(f'http://127.0.0.1:{port}/README.md', timeout=5) as response:
            self.assertEqual(response.read(), b'# Solo\n')
        environment = self.aw('run', 'app', 'sh', '-c', 'echo "$GREETING/$PORT"', cwd=lane, script=aw).stdout.strip()
        self.assertEqual(environment, f'lane 1 slot 1/{port}')
        report = json.loads(self.aw('whoami', '--json', cwd=lane, script=aw).stdout)
        self.assertEqual(report['slot']['number'], 1)
        self.assertEqual(report['slot']['urls'], {'app': f'http://localhost:{port}/'})
        self.aw('stop', cwd=lane, script=aw)
        with self.assertRaises(OSError):
            urllib.request.urlopen(f'http://127.0.0.1:{port}/', timeout=2)

        # Unmerged work keeps the lane, and a merge releases it. Nothing here consults a forge.
        (lane / 'feature.txt').write_text('work\n')
        git('add', 'feature.txt', cwd=lane)
        git('commit', '-qm', 'feature', cwd=lane)
        branch = git('branch', '--show-current', cwd=lane)
        os.kill(self.sleeper.pid, 9)
        self.sleeper.wait()
        self.assertNotIn('teardown', self.log.read_text())
        self.aw('sweep', '--apply')
        self.assertTrue(lane.exists())
        git('merge', '-q', '--no-ff', '-m', 'merge', branch, cwd=self.main)
        self.aw('delete', '1')
        self.assertFalse(lane.exists())
        self.assertIn('teardown lane=1', self.log.read_text())

    def test_guards_on_a_single_repo_lane(self):
        self.initialize()
        self.aw('attach')
        lane = self.main / '.lanes/1'
        self.wait_setup(1)

        def hook(root, command):
            payload = json.dumps({'cwd': str(lane), 'session_id': 'solo-session', 'tool_name': 'Bash', 'tool_input': {'command': command}})
            return subprocess.run([sys.executable, root / 'bin/aw', 'hook', 'pre-tool-use'], input=payload,
                                  env=self.env, text=True, capture_output=True).returncode

        self.assertEqual(hook(lane, f"cat '{lane}/README.md'"), 0)
        self.assertEqual(hook(lane, f"cat '{self.main}/README.md'"), 2)
        self.assertEqual(hook(lane, 'bin/aw delete 1'), 2)
        self.assertEqual(hook(lane, 'kill 1'), 2)

    def test_doctor_names_each_fix(self):
        self.initialize()
        (self.main / 'scripts/setup').chmod(0o644)
        (self.main / '.codex/hooks.json').unlink()
        report = json.loads(self.aw('doctor', '--json', ok=False).stdout)
        failed = {row['check']: row['fix'] for row in report['checks'] if not row['ok']}
        self.assertFalse(report['ok'])
        self.assertIn('setup script is executable', failed)
        self.assertEqual(failed['.codex/hooks.json routes to bin/aw'], 'bin/aw hooks-config')


class ScaffoldTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()

    def test_hook_merge_keeps_other_settings(self):
        path = self.root / '.claude/settings.json'
        path.parent.mkdir()
        path.write_text(json.dumps({'permissions': {'allow': ['Read']}, 'hooks': {'Old': []}}))
        scaffold.write_hooks(self.root)
        data = json.loads(path.read_text())
        self.assertEqual(data['permissions'], {'allow': ['Read']})
        self.assertIn('PreToolUse', data['hooks'])
        self.assertNotIn('Old', data['hooks'])
        self.assertEqual(scaffold.write_hooks(self.root, check=True), 0)
        path.write_text('{}')
        self.assertEqual(scaffold.write_hooks(self.root, check=True), 1)

    def test_instructions_replace_only_their_block(self):
        git('init', '-q', cwd=self.root)
        fixtures.write_workspace(self.root)
        (self.root / 'AGENTS.md').write_text('# Mine\n\nKeep this.\n')
        git('config', 'user.email', 'f@example.invalid', cwd=self.root)
        git('config', 'user.name', 'F', cwd=self.root)
        git('add', '.', cwd=self.root)
        git('commit', '-qm', 'x', cwd=self.root)
        context = Context.discover(self.root)
        scaffold.write_instructions(context)
        (self.root / 'AGENTS.md').write_text((self.root / 'AGENTS.md').read_text() + '\nAfter.\n')
        scaffold.write_instructions(context)
        text = (self.root / 'AGENTS.md').read_text()
        self.assertEqual(text.count(scaffold.BEGIN), 1)
        self.assertTrue(text.startswith('# Mine\n\nKeep this.\n'))
        self.assertTrue(text.endswith('After.\n'))
        self.assertEqual(scaffold.write_instructions(context, check=True), 0)


if __name__ == '__main__':
    unittest.main()
