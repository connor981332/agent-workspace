"""A synthetic two-project workspace for the suites. Docker is always a fake in these tests."""
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'src'))
from agent_workspace import scaffold

MANIFEST = r'''
[workspace]
name = "Fixture"
forge = "gitlab"

[lanes]
workspace_base = "master"
published_slots = 3

[docker]
compose_file = "infra/agents.yaml"
project = "aw-agents"
status_projects = ["aw-infra"]

[guards]
server_patterns = ['npm\s+run\s+dev', 'npm\s+start', 'next\s+(dev|start)', 'nodemon', 'tsx\s+watch', 'lib/server\.ts']

[[guards.commands]]
pattern = 'devcontainer-init'
message = 'Containers are shared; use bin/aw and owner-side maintenance commands.'

[lifecycle]
teardown = "scripts/teardown"

[tool]
maintenance_scripts = ["bootstrap"]

[projects.api]
directory = "project-api"
runner = "docker"
install = ["npm", "install", "--no-audit", "--no-fund"]
install_stamp = "package-lock.json"
generated = [".env", "lib/generated/", "node_modules/"]

[projects.api.docker]
container = "aw-agents-agent-api-1"
service = "agent-api"
user = "node"

[projects.api.devcontainer]
service = "aw-api"

[projects.api.server]
command = "NODE_ENV=development DEBUG=app,app:* exec ./node_modules/.bin/tsx watch lib/server.ts"
port = 4050
owner_port = 3050
url = "http://localhost:{port}/api/v1"

[projects.api.server.env]
PORT = "{port}"
OAUTH_PUBLIC_URL = "http://localhost:{port}"
OAUTH_CONSENT_URL = "http://localhost:{ports[next]}/oauth/consent"

[projects.next]
directory = "project-next"
runner = "docker"
install = ["npm", "install", "--no-audit", "--no-fund"]
install_stamp = "package-lock.json"
generated = [".env.development", ".env.test.local", "node_modules/"]

[projects.next.docker]
container = "aw-agents-agent-next-1"
service = "agent-next"
user = "node"

[projects.next.devcontainer]
service = "aw-next"

[projects.next.server]
command = "exec npm run dev"
port = 4000
owner_port = 3000
after = ["api"]
log = "logs/server.log"

[projects.next.server.env]
PORT = "{port}"
AW_API_PORT = "{ports[api]}"
AW_API_PUBLIC_URL = "http://localhost:{ports[api]}"
PLAYWRIGHT_BASE_URL = "http://localhost:{port}"
'''

# Stands in for a real workspace's data removal, which is what the cleanup suites observe.
TEARDOWN = '''#!/bin/sh
set -eu
for suffix in "" -test; do
  docker exec database psql -qc "DROP DATABASE IF EXISTS \\"aw-lane$AW_LANE$suffix\\""
done
'''

GITIGNORE = '/project-api/\n/project-next/\n/.lane\n/.lanes/\n/.worktrees/\n__pycache__/\n*.pyc\n'


def write_workspace(root, manifest=MANIFEST):
    """The committed files every workspace carries, generated the way aw init generates them."""
    root = Path(root)
    (root / 'workspace.toml').write_text(manifest)
    (root / '.gitignore').write_text(GITIGNORE)
    (root / 'infra').mkdir(exist_ok=True)
    (root / 'infra/agents.yaml').write_text('name: aw-agents\n')
    (root / 'scripts').mkdir(exist_ok=True)
    (root / 'scripts/teardown').write_text(TEARDOWN)
    (root / 'scripts/teardown').chmod(0o755)
    scaffold.install_launcher(root)
    scaffold.write_hooks(root)
