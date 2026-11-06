"""workspace.toml: everything that is specific to one workspace.

The core never names a stack, a database or an application. Anything a workspace needs beyond
worktrees, slots, sessions and cleanup proof belongs here or in its lifecycle scripts. Unknown
keys are errors, so a typo cannot silently weaken a guard. See docs/manifest.md.
"""
from dataclasses import dataclass, field
from pathlib import Path
import re
import shlex
import tomllib

NAME = 'workspace.toml'
ALIAS = re.compile(r'[a-z][a-z0-9_-]*\Z')
NAMESPACE = re.compile(r'[a-z][a-z0-9]{0,15}\Z')
TASK = re.compile(r'[a-z][a-z0-9-]*\Z')
RESERVED_ALIASES = {'all', 'both', 'workspace'}
FORGES = ('auto', 'github', 'gitlab', 'none')
RUNNERS = ('host', 'docker')
LANE_KEYS = {'directory', 'slots', 'published_slots', 'count_owner_servers', 'max_lanes',
             'slot_idle_minutes', 'evict_grace_seconds', 'workspace_base', 'project_base',
             'lane_pool', 'lane_retention_days', 'port_stride'}


def _table(data, where, allowed):
    if not isinstance(data, dict):
        raise ValueError(f'{where}: expected a table')
    unknown = sorted(set(data) - set(allowed))
    if unknown:
        raise ValueError(f'{where}: unknown keys {unknown}')
    return data


def _strings(value, where):
    if not isinstance(value, list) or any(not isinstance(v, str) or not v for v in value):
        raise ValueError(f'{where}: expected a list of strings')
    return tuple(value)


def _command(value, where):
    """A command is an argv list, or a string split once with shell rules and never evaluated."""
    if isinstance(value, str):
        value = shlex.split(value)
    words = _strings(value, where)
    if not words:
        raise ValueError(f'{where}: empty command')
    return words


@dataclass(frozen=True)
class Server:
    command: str
    port: int
    owner_port: int | None = None
    url: str = 'http://localhost:{port}'
    ready_path: str = '/'
    after: tuple = ()
    env: dict = field(default_factory=dict)
    log: str | None = None


@dataclass(frozen=True)
class Project:
    alias: str
    directory: str
    base: str | None = None
    repo: str | None = None
    runner: str = 'host'
    install: tuple = ()
    install_stamp: str | None = None
    generated: tuple = ()
    server: Server | None = None
    container: str | None = None
    service: str | None = None
    user: str | None = None
    devcontainer_service: str | None = None
    devcontainer_workdir: str | None = None
    extra_env: dict = field(default_factory=dict)

    @property
    def nested(self):
        """A nested project is its own Git repository; '.' shares the workspace repository."""
        return self.directory != '.'


@dataclass(frozen=True)
class Task:
    name: str
    command: tuple
    quiesce: tuple = ()
    description: str = ''


@dataclass(frozen=True)
class Guard:
    pattern: str
    message: str


@dataclass(frozen=True)
class Manifest:
    name: str
    namespace: str
    forge: str
    lanes: dict
    projects: dict
    compose_file: str | None = None
    compose_project: str | None = None
    before_up: tuple = ()
    status_projects: tuple = ()
    prepare: tuple = ()
    setup: tuple = ()
    teardown: tuple = ()
    tasks: dict = field(default_factory=dict)
    guards: tuple = ()
    server_patterns: tuple = ()
    shims: tuple = ()
    maintenance_scripts: tuple = ()
    plan_folders: tuple = ()
    ignored_link_prefixes: tuple = ()
    tool: dict = field(default_factory=dict)

    @property
    def env_prefix(self):
        return self.namespace.upper() + '_'

    def servers(self, wanted=None):
        """Projects with servers in start order. `wanted` also pulls in their dependencies."""
        chosen = set(self.projects) if wanted is None else set(wanted)
        order, seen = [], set()

        def visit(alias, trail=()):
            if alias in trail:
                raise ValueError('Server dependency cycle: ' + ' -> '.join((*trail, alias)))
            if alias in seen:
                return
            project = self.projects[alias]
            if project.server:
                for dependency in project.server.after:
                    visit(dependency, (*trail, alias))
                order.append(alias)
            seen.add(alias)

        for alias in self.projects:
            if alias in chosen:
                visit(alias)
        return order


def _server(alias, data):
    where = f'projects.{alias}.server'
    _table(data, where, ('command', 'port', 'owner_port', 'url', 'ready_path', 'after', 'env', 'log'))
    if not isinstance(data.get('command'), str) or not data['command']:
        raise ValueError(where + ': command is required')
    if type(data.get('port')) is not int or not 0 < data['port'] < 65536:
        raise ValueError(where + ': port must be the base port')
    env = data.get('env', {})
    if not isinstance(env, dict) or any(not isinstance(v, str) for v in env.values()):
        raise ValueError(where + ': env must map names to strings')
    return Server(data['command'], data['port'], data.get('owner_port'),
                  data.get('url', 'http://localhost:{port}'), data.get('ready_path', '/'),
                  _strings(data.get('after', []), where + '.after'), env, data.get('log'))


def _project(alias, data):
    where = f'projects.{alias}'
    if not ALIAS.fullmatch(alias) or alias in RESERVED_ALIASES:
        raise ValueError(where + ': invalid project alias')
    _table(data, where, ('directory', 'base', 'repo', 'runner', 'install', 'install_stamp', 'generated',
                         'server', 'docker', 'devcontainer'))
    directory = data.get('directory', alias)
    if directory != '.' and (Path(directory).name != directory or directory in ('', '..')):
        raise ValueError(where + ': directory must be "." or one path component')
    runner = data.get('runner', 'host')
    if runner not in RUNNERS:
        raise ValueError(where + f': runner must be one of {RUNNERS}')
    docker = _table(data.get('docker', {}), where + '.docker', ('container', 'service', 'user'))
    if runner == 'docker' and not all(docker.get(k) for k in ('container', 'service')):
        raise ValueError(where + '.docker: container and service are required for the docker runner')
    dev = _table(data.get('devcontainer', {}), where + '.devcontainer', ('service', 'workdir'))
    return Project(alias, directory, data.get('base'), data.get('repo'), runner,
                   _command(data['install'], where + '.install') if 'install' in data else (),
                   data.get('install_stamp'), _strings(data.get('generated', []), where + '.generated'),
                   _server(alias, data['server']) if 'server' in data else None,
                   docker.get('container'), docker.get('service'), docker.get('user'),
                   dev.get('service'), dev.get('workdir'))


def _task(name, data):
    where = f'tasks.{name}'
    if not TASK.fullmatch(name):
        raise ValueError(where + ': invalid task name')
    _table(data, where, ('command', 'quiesce', 'description'))
    return Task(name, _command(data.get('command', ()), where + '.command'),
                _strings(data.get('quiesce', []), where + '.quiesce'), data.get('description', ''))


def parse(data):
    _table(data, NAME, ('workspace', 'lanes', 'projects', 'docker', 'lifecycle', 'tasks',
                        'guards', 'docs', 'tool'))
    workspace = _table(data.get('workspace', {}), 'workspace', ('name', 'namespace', 'forge'))
    namespace = workspace.get('namespace', 'aw')
    if not isinstance(namespace, str) or not NAMESPACE.fullmatch(namespace):
        raise ValueError('workspace.namespace must be 1-16 lowercase letters or digits')
    forge = workspace.get('forge', 'auto')
    if forge not in FORGES:
        raise ValueError(f'workspace.forge must be one of {FORGES}')
    lanes = _table(data.get('lanes', {}), 'lanes', LANE_KEYS)
    projects_data = data.get('projects', {})
    if not isinstance(projects_data, dict) or not projects_data:
        raise ValueError('At least one [projects.<alias>] table is required')
    projects = {alias: _project(alias, value) for alias, value in projects_data.items()}
    directories = [p.directory for p in projects.values()]
    if len(set(directories)) != len(directories):
        raise ValueError('Project directories must be unique')
    if '.' in directories and len(directories) > 1:
        raise ValueError('A "." project is the whole workspace, so it must be the only project')
    for project in projects.values():
        for dependency in project.server.after if project.server else ():
            if dependency not in projects or not projects[dependency].server:
                raise ValueError(f'projects.{project.alias}.server.after: no server named {dependency}')
    docker = _table(data.get('docker', {}), 'docker', ('compose_file', 'project', 'before_up', 'status_projects'))
    if any(p.runner == 'docker' for p in projects.values()) and not all(docker.get(k) for k in ('compose_file', 'project')):
        raise ValueError('docker: compose_file and project are required by the docker runner')
    lifecycle = _table(data.get('lifecycle', {}), 'lifecycle', ('prepare', 'setup', 'teardown'))
    guards = _table(data.get('guards', {}), 'guards', ('server_patterns', 'commands'))
    rows = []
    for index, row in enumerate(guards.get('commands', [])):
        _table(row, f'guards.commands[{index}]', ('pattern', 'message'))
        if not row.get('pattern') or not row.get('message'):
            raise ValueError(f'guards.commands[{index}]: pattern and message are required')
        re.compile(row['pattern'])
        rows.append(Guard(row['pattern'], row['message']))
    patterns = _strings(guards.get('server_patterns', []), 'guards.server_patterns')
    for pattern in patterns:
        re.compile(pattern)
    docs = _table(data.get('docs', {}), 'docs', ('plan_folders', 'ignored_link_prefixes'))
    tool = _table(data.get('tool', {}), 'tool', ('source', 'ref', 'subdir', 'shims', 'maintenance_scripts'))
    command = lambda key: _command(lifecycle[key], 'lifecycle.' + key) if key in lifecycle else ()
    tasks = {name: _task(name, value) for name, value in data.get('tasks', {}).items()}
    for task in tasks.values():
        for alias in task.quiesce:
            if alias not in projects or not projects[alias].server:
                raise ValueError(f'tasks.{task.name}.quiesce: no server named {alias}')
    return Manifest(workspace.get('name', 'Workspace'), namespace, forge, lanes, projects,
                    docker.get('compose_file'), docker.get('project'),
                    _command(docker['before_up'], 'docker.before_up') if 'before_up' in docker else (),
                    _strings(docker.get('status_projects', []), 'docker.status_projects'),
                    command('prepare'), command('setup'), command('teardown'), tasks, tuple(rows), patterns,
                    _strings(tool.get('shims', []), 'tool.shims'),
                    _strings(tool.get('maintenance_scripts', []), 'tool.maintenance_scripts'),
                    _strings(docs.get('plan_folders', []), 'docs.plan_folders'),
                    _strings(docs.get('ignored_link_prefixes', []), 'docs.ignored_link_prefixes'),
                    {k: tool[k] for k in ('source', 'ref', 'subdir') if k in tool})


def load(root):
    path = Path(root) / NAME
    try:
        data = tomllib.loads(path.read_text())
    except FileNotFoundError:
        raise ValueError(f'Missing {NAME} in {root}; run aw init there') from None
    except tomllib.TOMLDecodeError as exc:
        raise ValueError(f'{path}: {exc}') from exc
    return parse(data)
