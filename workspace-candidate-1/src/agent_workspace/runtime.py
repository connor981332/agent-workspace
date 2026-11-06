"""Shared command execution and slot runtime. All mutation is explicit."""
from contextlib import contextmanager
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

from . import procinfo
from .context import command, git
from .evidence import lock, locked_file
from .inspection import observe_sessions


def write_text(path, text):
    path = Path(path)
    if path.is_symlink():
        raise ValueError(f'Refusing symlink state: {path}')
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f'.{path.name}.{os.getpid()}.tmp')
    try:
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, 'w') as f:
            f.write(text)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        tmp.unlink(missing_ok=True)


def checked(args, **kwargs):
    result = command(args, **kwargs)
    if result.returncode:
        raise ValueError(result.stderr.strip() or f'Command failed: {args[0]}')
    return result.stdout


def forward(args, cwd=None, env=None):
    """Preserve argv/streams and forward termination without leaking a managed child."""
    process = subprocess.Popen(list(map(str, args)), cwd=cwd, env=env)
    previous = signal.getsignal(signal.SIGTERM)
    def terminate(signum, frame):
        if process.poll() is None:
            process.send_signal(signum)
    signal.signal(signal.SIGTERM, terminate)
    try:
        try:
            status = process.wait()
        except KeyboardInterrupt:
            if process.poll() is None:
                process.send_signal(signal.SIGINT)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.terminate()
                process.wait(timeout=5)
            return 130
        return status if status >= 0 else 128 - status
    finally:
        signal.signal(signal.SIGTERM, previous)


def read_env_file(path):
    """The KEY=VALUE format shared with `docker exec --env-file`: no quoting, no expansion."""
    values = {}
    for line in Path(path).read_text().splitlines():
        if line.strip() and not line.lstrip().startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            values[key.strip()] = value
    return values


class HostRunner:
    """Commands run directly on this machine, in the lane's project directory."""

    def __init__(self, runtime, project):
        self.r, self.project = runtime, project

    def directory(self, n):
        return self.r.c.lane(n) / self.project.directory

    def environment(self, n):
        env_file, overrides = self.r.env(n, self.project.alias)
        values = {**os.environ, **(read_env_file(env_file) if env_file else {})}
        return {**values, **overrides}

    def execute(self, n, args):
        return forward(args, cwd=self.directory(n), env=self.environment(n))

    def start(self, n, cmd, pidfile, log):
        with Path(log).open('ab') as out:
            process = subprocess.Popen(['sh', '-c', cmd], cwd=self.directory(n), env=self.environment(n),
                                       stdin=subprocess.DEVNULL, stdout=out, stderr=subprocess.STDOUT,
                                       start_new_session=True)
        write_text(pidfile, str(process.pid))

    def process(self, pid):
        start = procinfo.start_identity(pid)
        if start is None:
            return None
        try:
            return pid, start, os.getpgid(pid)
        except ProcessLookupError:
            return None

    def signal(self, group, number):
        try:
            os.killpg(group, number)
        except ProcessLookupError:
            pass

    def reachable(self, port, path):
        try:
            with urllib.request.urlopen(f'http://localhost:{port}{path}', timeout=3):
                return True
        except urllib.error.HTTPError:
            return True  # Any HTTP answer means the server is listening.
        except (OSError, ValueError):
            return False

    def owner_listening(self, port):
        with socket.socket() as probe:
            probe.settimeout(1)
            return probe.connect_ex(('127.0.0.1', port)) == 0


class DockerRunner:
    """Commands run in one shared container per project, at the lane's own absolute path."""

    def __init__(self, runtime, project):
        self.r, self.project = runtime, project

    @property
    def container(self):
        return self.project.container

    def directory(self, n):
        return self.r.c.lane(n) / self.project.directory

    def env_args(self, n):
        env_file, overrides = self.r.env(n, self.project.alias)
        args = ['--env-file', str(env_file)] if env_file else []
        for key, value in overrides.items():
            args += ['-e', f'{key}={value}']
        return args

    def user_args(self):
        return ['-u', self.project.user] if self.project.user else []

    def execute(self, n, args):
        tty = '-it' if sys.stdin.isatty() and sys.stdout.isatty() else '-i'
        return forward(['docker', 'exec', tty, *self.user_args(), '-w', self.directory(n),
                        *self.env_args(n), self.container, *args])

    def start(self, n, cmd, pidfile, log):
        checked(['docker', 'exec', '-d', *self.user_args(), '-w', self.directory(n),
                 *self.env_args(n), self.container, 'setsid', 'sh', '-c',
                 'echo $$ > "$0"; exec sh -c "$1" >> "$2" 2>&1', pidfile, cmd, log])

    def process(self, pid):
        result = command(['docker', 'exec', self.container, 'cat', f'/proc/{pid}/stat'])
        if result.returncode:
            if 'No such file' in result.stderr:
                return None
            raise ValueError('Cannot verify container server state; retaining ownership markers')
        stat = result.stdout.rsplit(')', 1)[1].split()
        return pid, stat[19], int(stat[2])  # start identity and process group

    def signal(self, group, number):
        name = {signal.SIGTERM: 'TERM', signal.SIGKILL: 'KILL'}[number]
        checked(['docker', 'exec', self.container, 'kill', '-' + name, f'-{group}'])

    def reachable(self, port, path):
        return not command(['docker', 'exec', self.container, 'curl', '-s', '-o', '/dev/null',
                            '--max-time', '3', f'http://localhost:{port}{path}']).returncode

    def owner_listening(self, port):
        project = self.project
        name = f'{project.directory}_devcontainer-{project.devcontainer_service}-1'
        result = command(['docker', 'exec', name, 'netstat', '-ltn'])
        return result.returncode == 0 and bool(re.search(rf'[:.]{port}\s', result.stdout))


class Runtime:
    def __init__(self, context):
        self.c = context
        self.m = context.manifest

    def initialize(self):
        (self.c.state / 'slots').mkdir(parents=True, exist_ok=True)
        (self.c.state / 'lanes').mkdir(exist_ok=True)
        (self.c.state / 'queue').touch(exist_ok=True)

    def runner(self, alias):
        if alias not in self.c.projects:
            raise ValueError('Unknown project')
        project = self.m.projects[alias]
        return (DockerRunner if project.runner == 'docker' else HostRunner)(self, project)

    def container(self, alias):
        runner = self.runner(alias)
        if not isinstance(runner, DockerRunner):
            raise ValueError(f'{alias} does not run in a container')
        return runner.container

    @property
    def docker_projects(self):
        return [p for p in self.m.projects.values() if p.runner == 'docker']

    def leases(self):
        leases = {}
        for p in sorted((self.c.state / 'slots').glob('*')):
            if p.is_symlink() or not p.name.isdigit():
                raise ValueError('Invalid slot record')
            values = dict(line.split('=', 1) for line in p.read_text().splitlines() if '=' in line)
            if not re.fullmatch(r'owner|lane[1-9][0-9]*', values.get('holder', '')):
                raise ValueError('Unknown slot holder; reconcile manually')
            leases[int(p.name)] = values
        return leases

    def slot(self, number):
        return next((k for k, v in self.leases().items() if v['holder'] == f'lane{number}'), None)

    def port(self, alias, k):
        server = self.m.projects[alias].server
        return server.port + self.c.settings['port_stride'] * k

    def slot_ports(self, k):
        return {a: self.port(a, k) for a in self.m.servers()}

    def template(self, text, alias, n, k):
        return text.format(port=self.port(alias, k), ports=self.slot_ports(k), lane=n, slot=k)

    def env(self, n, alias):
        """Environment for one project command: the lane's env file, then slot-derived values."""
        path = self.c.lane_state(n) / f'{alias}.env'
        overrides = {}
        env_file = path if path.exists() else None
        if not env_file:
            overrides[self.m.env_prefix + 'LANE'] = str(n)
        k = self.slot(n)
        server = self.m.projects[alias].server
        if k is not None and server:
            overrides.update({key: self.template(value, alias, n, k) for key, value in server.env.items()})
        return env_file, overrides

    def check_mounts(self):
        docker = self.docker_projects
        if not docker:
            return
        expected = {str(self.c.lanes), str(self.c.main / '.git')}
        expected.update(str(self.c.main / d / '.git') for d, _ in self.c.nested_projects)
        for project in docker:
            result = command(['docker', 'inspect', project.container, '--format',
                              '{{range .Mounts}}{{.Source}}:{{.Destination}}{{println}}{{end}}'])
            if result.returncode:
                continue
            mounts = set(result.stdout.split())
            if mounts != {f'{p}:{p}' for p in expected}:
                raise ValueError('Agent container has stale or unexpected mounts; inspect before recreation')

    def compose(self, *args):
        prefix = self.m.env_prefix
        result = subprocess.run(['docker', 'compose', '-f', str(self.c.main / self.m.compose_file), *args],
                                env={**os.environ, prefix + 'MAIN': str(self.c.main), prefix + 'LANES_DIR': str(self.c.lanes)})
        if result.returncode:
            raise ValueError('Agent container operation failed')

    def ensure_containers(self):
        docker = self.docker_projects
        if not docker:
            return
        self.check_mounts()
        running = command(['docker', 'ps', '--filter', f'label=com.docker.compose.project={self.m.compose_project}',
                           '--filter', 'status=running', '--format', '{{.Label "com.docker.compose.service"}}'])
        if set(running.stdout.split()) != {p.service for p in docker}:
            if self.m.before_up and forward([self.c.root / self.m.before_up[0], *self.m.before_up[1:]]):
                raise ValueError('Infrastructure startup failed')
            self.compose('up', '-d', '--no-recreate')
            self.check_mounts()

    def setup_wait(self, n):
        state = self.c.lane_state(n)
        if locked_file(state / 'setup.lock'):
            print(f'Waiting for lane {n} setup; log: {state / "setup.log"}', file=sys.stderr)
            with lock(state / 'setup.lock'):
                pass
        if not (state / 'setup.ok').is_file():
            raise ValueError(f'Lane setup incomplete; inspect {state / "setup.log"} and run {self.c.tool} setup {n}')

    def execute(self, n, alias, args, *, busy=True):
        state = self.c.lane_state(n)
        marker = state / f'busy.{os.getpid()}'
        held = busy and self.slot(n) is not None
        if held:
            write_text(marker, str(os.getpid()))
        try:
            return self.runner(alias).execute(n, args)
        finally:
            if held:
                marker.unlink(missing_ok=True)

    def refuse_servers(self, args):
        text = ' '.join(args)
        if any(re.search(pattern, text) for pattern in self.m.server_patterns):
            raise ValueError(f'Start servers with {self.c.tool} serve; shared resources require a slot')

    def owner_execute(self, alias, args):
        project = self.m.projects[alias]
        if not project.devcontainer_service:
            directory = self.c.root / project.directory
            if args == ['--up']:
                return 0
            return forward(args or [os.environ.get('SHELL', 'sh')], cwd=directory)
        directory, service = project.directory, project.devcontainer_service
        def find():
            ids = checked(['docker', 'ps', '-q', '--filter', f'label=com.docker.compose.project={directory}_devcontainer',
                           '--filter', f'label=com.docker.compose.service={service}',
                           '--filter', f'label=devcontainer.local_folder={self.c.root / directory}']).split()
            if len(ids) > 1:
                raise ValueError(f'Multiple owner containers match; inspect {self.c.tool} status')
            return ids[0] if ids else None
        identifier = find()
        if not identifier:
            status = forward(['npx', '-y', '@devcontainers/cli@0.89.0', 'up', '--workspace-folder', self.c.root / directory])
            if status:
                return status
            identifier = find()
            if not identifier:
                raise ValueError('Devcontainer startup did not produce the expected container')
        if args == ['--up']:
            return 0
        tty = '-it' if sys.stdin.isatty() and sys.stdout.isatty() else '-i'
        user = ['-u', project.user] if project.user else []
        workdir = project.devcontainer_workdir or f'/{service}'
        return forward(['docker', 'exec', tty, *user, '-w', workdir, identifier, *(args or ['bash'])])

    def status(self):
        result = 0
        names = [*self.m.status_projects, *([self.m.compose_project] if self.m.compose_project else []),
                 *(p.directory + '_devcontainer' for p in self.m.projects.values() if p.devcontainer_service)]
        if not names:
            print('No containers are configured for this workspace.')
            return 0
        for project in names:
            result = forward(['docker', 'ps', '-a', '--filter', 'label=com.docker.compose.project=' + project,
                              '--format', '{{.Names}}\t{{.Status}}\t{{.Label "devcontainer.local_folder"}}']) or result
        return result

    def busy(self, n):
        for p in self.c.lane_state(n).glob('busy.*'):
            try:
                os.kill(int(p.name.split('.')[1]), 0)
                return True
            except ProcessLookupError:
                continue
            except (ValueError, OSError):
                return True
        return False

    def session_active(self, n):
        return locked_file(self.c.lane_state(n) / 'session.lock') or any(
            s.get('state') != 'Ended' for s in observe_sessions(self.c.state, n))

    def server_identity(self, n, alias):
        path = self.c.lane_state(n) / f'{alias}.pid'
        if not path.exists():
            return None
        pid = int(path.read_text().strip())
        if pid <= 1:
            raise ValueError('Invalid server PID')
        return self.runner(alias).process(pid)

    def stop_server(self, n, alias):
        state = self.c.lane_state(n)
        identity = self.server_identity(n, alias)
        if identity:
            pid, start, group = identity
            recorded = state / f'{alias}.start'
            if not recorded.is_file() or recorded.read_text().strip() != start or pid != group:
                raise ValueError('Server process ownership is uncertain; refusing to signal it')
            runner = self.runner(alias)
            runner.signal(pid, signal.SIGTERM)
            for _ in range(10):
                if self.server_identity(n, alias) != identity:
                    break
                time.sleep(1)
            if self.server_identity(n, alias) == identity:
                runner.signal(pid, signal.SIGKILL)
        (state / f'{alias}.pid').unlink(missing_ok=True)
        (state / f'{alias}.start').unlink(missing_ok=True)

    def start_server(self, n, alias):
        if self.server_identity(n, alias):
            return
        k = self.slot(n)
        if k is None:
            raise ValueError('Lane lost its slot')
        state = self.c.lane_state(n)
        server = self.m.projects[alias].server
        port = self.port(alias, k)
        runner = self.runner(alias)
        write_text(state / f'{alias}.log', '')
        runner.start(n, server.command, state / f'{alias}.pid', state / f'{alias}.log')
        deadline = time.monotonic() + 180
        while time.monotonic() < deadline:
            if self.slot(n) != k:
                raise ValueError('Lane lost its slot during server startup')
            identity = self.server_identity(n, alias)
            if identity:
                write_text(state / f'{alias}.start', identity[1])
            if runner.reachable(port, server.ready_path):
                if not identity:
                    raise ValueError('Port answered without a verifiable lane server')
                return
            if (state / f'{alias}.pid').exists() and identity is None:
                raise ValueError(f'{alias} exited; inspect {state / (alias + ".log")}')
            time.sleep(2)
        raise ValueError(f'{alias} startup timed out; inspect its log')

    def release_locked(self, n, notice=None):
        k = self.slot(n)
        if k:
            for alias in reversed(self.m.servers()):
                self.stop_server(n, alias)
            (self.c.state / 'slots' / str(k)).unlink()
        self.dequeue(n)
        if notice:
            path = self.c.lane_state(n) / 'notice'
            write_text(path, (path.read_text() if path.exists() else '') + notice + '\n')

    def stop(self, n):
        with lock(self.c.state / 'lock'):
            self.release_locked(n)

    def queue(self):
        path = self.c.state / 'queue'
        rows = []
        for line in path.read_text().splitlines() if path.exists() else []:
            row = tuple(map(int, line.split()))
            if len(row) != 4 or row[2] < 1 or row[3] < 1:
                raise ValueError('Invalid queue record')
            rows.append(row)
        return rows

    def save_queue(self, rows):
        write_text(self.c.state / 'queue', ''.join(' '.join(map(str, r)) + '\n' for r in rows))

    def dequeue(self, n):
        self.save_queue([r for r in self.queue() if r[2] != n])

    def owner_running(self):
        if not self.c.settings['count_owner_servers']:
            return False
        for alias in self.m.servers():
            project = self.m.projects[alias]
            if project.server.owner_port and (project.runner == 'host' or project.devcontainer_service):
                if self.runner(alias).owner_listening(project.server.owner_port):
                    return True
        return False

    def activity_files(self, n):
        state = self.c.lane_state(n)
        files = [state / 'last']
        for alias in self.m.servers():
            files.append(state / f'{alias}.log')
            log = self.m.projects[alias].server.log
            if log:
                files.append(self.c.lane(n) / self.m.projects[alias].directory / log)
        return files

    def reconcile_locked(self):
        rows = []
        for row in self.queue():
            try:
                os.kill(row[3], 0)
                rows.append(row)
            except ProcessLookupError:
                pass
        self.save_queue(rows)
        now = time.time()
        for k, lease in self.leases().items():
            granted = int(lease['granted'])
            if lease['holder'] == 'owner':
                seen = self.c.state / 'owner-seen'
                if self.owner_running():
                    seen.touch()
                if now - max(granted, seen.stat().st_mtime if seen.exists() else granted) > self.c.settings['slot_idle_minutes'] * 60:
                    (self.c.state / 'slots' / str(k)).unlink()
                continue
            n = int(lease['holder'][4:])
            if self.busy(n) or now - granted < 120:
                continue
            latest = max([p.stat().st_mtime for p in self.activity_files(n) if p.exists()] or [granted])
            idle = now - latest
            if (not any(self.server_identity(n, a) for a in self.m.servers())
                    or idle > self.c.settings['slot_idle_minutes']*60 or (idle > 300 and not self.session_active(n))):
                self.release_locked(n, f'Your server slot expired or the session ended. Run {self.c.tool} serve again.')

    def try_grant(self, n):
        self.reconcile_locked()
        existing = self.slot(n)
        if existing:
            return existing, 0
        state = self.c.lane_state(n)
        priority = 0 if (state / 'priority').exists() else 1
        rows = self.queue()
        old = next((r for r in rows if r[2] == n), None)
        rows = [r for r in rows if r[2] != n] + [(priority, old[1] if old else int(time.time()), n, os.getpid())]
        rows.sort()
        self.save_queue(rows)
        leases = self.leases()
        used = len(leases) + int(not any(v['holder'] == 'owner' for v in leases.values()) and self.owner_running())
        if rows[0][2] == n and used < self.c.settings['slots']:
            k = next(k for k in range(1, self.c.settings['slots']+1) if k not in leases)
            write_text(self.c.state / 'slots' / str(k), f'holder=lane{n}\ngranted={int(time.time())}\n')
            self.dequeue(n)
            (state / 'priority').unlink(missing_ok=True)
            return k, 0
        return None, next(i+1 for i, r in enumerate(rows) if r[2] == n)

    def serve(self, n, wanted=None, wait=True):
        order = self.m.servers(wanted)
        if not order:
            raise ValueError('No project in this workspace defines a server')
        marker = self.c.lane_state(n) / f'busy.{os.getpid()}'
        try:
            while True:
                with lock(self.c.state / 'lock'):
                    k, position = self.try_grant(n)
                    if k:
                        write_text(marker, str(os.getpid()))
                if k:
                    break
                if not wait:
                    return 3
                print(f'Waiting for slot: queue position {position}', file=sys.stderr)
                time.sleep(5)
            for alias in order:
                self.start_server(n, alias)
            print(f'Lane {n} · slot {k}')
            for alias in order:
                print(f'{alias}: ' + self.template(self.m.projects[alias].server.url, alias, n, k))
            return 0
        finally:
            marker.unlink(missing_ok=True)
            with lock(self.c.state / 'lock'):
                self.dequeue(n)

    def mine(self, done=False):
        if self.c.caller:
            raise ValueError('mine is owner-only')
        deadline = time.monotonic() + self.c.settings['evict_grace_seconds']
        while True:
            with lock(self.c.state / 'lock'):
                self.reconcile_locked()
                leases = self.leases()
                owner = next((k for k, v in leases.items() if v['holder'] == 'owner'), None)
                if done:
                    if owner:
                        (self.c.state / 'slots' / str(owner)).unlink()
                    return 0
                if owner:
                    return 0
                if len(leases) < self.c.settings['slots']:
                    k = next(k for k in range(1, self.c.settings['slots']+1) if k not in leases)
                else:
                    candidates = sorted((self.busy(int(v['holder'][4:])), int(v['granted']), k, int(v['holder'][4:]))
                                        for k, v in leases.items() if v['holder'].startswith('lane'))
                    if not candidates:
                        raise ValueError('No agent slot available')
                    busy, _, k, n = candidates[0]
                    if busy and time.monotonic() < deadline:
                        k = None
                    else:
                        (self.c.lane_state(n) / 'priority').touch()
                        self.release_locked(n, f'The owner took your slot. Run {self.c.tool} serve to regain priority.')
                if k:
                    write_text(self.c.state / 'slots' / str(k), f'holder=owner\ngranted={int(time.time())}\n')
                    (self.c.state / 'owner-seen').touch()
                    return 0
            time.sleep(3)

    def script_env(self, n=None):
        """The contract every lifecycle script and task receives. See docs/manifest.md."""
        env = {**os.environ, 'AW_NAMESPACE': self.m.namespace, 'AW_MAIN': str(self.c.main),
               'AW_LANES_DIR': str(self.c.lanes), 'AW_SHARED_STATE': str(self.c.state),
               'AW_TOOL': str(self.c.main / 'bin/aw')}
        if n is not None:
            env.update(AW_LANE=str(n), AW_LANE_PATH=str(self.c.lane(n)), AW_LANE_STATE=str(self.c.lane_state(n)),
                       AW_TOOL=str(self.c.lane(n) / 'bin/aw'))
            k = self.slot(n)
            if k is not None:
                env['AW_SLOT'] = str(k)
                for alias, port in self.slot_ports(k).items():
                    env['AW_PORT_' + re.sub(r'[^A-Z0-9]', '_', alias.upper())] = str(port)
        return env

    def task(self, n, name, arguments=()):
        task = self.m.tasks.get(name)
        if not task:
            raise ValueError(f'Unknown task {name}; known: {sorted(self.m.tasks)}')
        restart = [a for a in task.quiesce if self.server_identity(n, a)]
        for alias in restart:
            self.stop_server(n, alias)
        env = {**self.script_env(n), 'AW_TASK': name}
        status = forward([*task.command, *arguments], cwd=self.c.lane(n), env=env)
        if not status:
            for alias in restart:
                self.start_server(n, alias)
        return status
