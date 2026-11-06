"""Read-only change-request evidence from a code forge.

Cleanup treats "merged" as permission to delete work, so every path here fails closed: anything
short of one exact match on the lane's head reports an unknown or absent availability, and
callers keep the lane. Forks are excluded because a fork's branch can reuse a lane branch name.

A result dict always has `availability`. A verified result also has project, iid, url,
source_head, state ('opened', 'merged' or 'closed') and draft.
"""
import json
import subprocess
from urllib.parse import quote


def _single(exact, matched):
    if len(exact) == 1:
        return {'availability': 'verified', **exact[0]}
    return {'availability': 'ambiguous' if len(exact) > 1 else 'source head mismatch' if matched else 'confirmed absent'}


class Forge:
    label = 'Forge'
    binary = None

    def __init__(self, deadline_left):
        self.deadline_left = deadline_left
        self.cache = {}

    def call(self, args, key):
        if key not in self.cache:
            if len(self.cache) >= 64:
                return None
            remaining = self.deadline_left()
            if remaining <= 0:
                return None
            try:
                result = subprocess.run(args, capture_output=True, text=True, timeout=min(5, remaining))
            except (OSError, subprocess.TimeoutExpired):
                result = subprocess.CompletedProcess(args, 127, '', self.label + ' unavailable')
            try:
                self.cache[key] = json.loads(result.stdout) if result.returncode == 0 else None
            except ValueError:
                self.cache[key] = None
        return self.cache[key]

    def change(self, source, branch, head):
        raise NotImplementedError


class GitLab(Forge):
    label = 'GitLab'
    binary = 'glab'

    def api(self, identity, endpoint):
        path = f"projects/{quote(identity['project'], safe='')}" + ('/' + endpoint if endpoint else '')
        return self.call(['glab', 'api', '--hostname', identity['host'], path],
                         (identity['host'], identity['project'], endpoint))

    def change(self, source, branch, head):
        matches = []
        for page in range(1, 6):
            data = self.api(source, 'merge_requests?state=all&scope=all&source_branch='
                            + quote(branch, safe='') + f'&per_page=100&page={page}')
            if not isinstance(data, list):
                return {'availability': 'unknown (GitLab unavailable)'}
            matches += [m for m in data if m.get('source_branch') == branch
                        and m.get('target_branch') == source['target']]
            if len(data) < 100:
                break
        else:
            return {'availability': 'unknown (pagination limit)'}
        exact = []
        for m in matches:
            project = self.api(source, '')
            if not isinstance(project, dict) or m.get('source_project_id') != project.get('id') or m.get('target_project_id') != project.get('id'):
                continue
            detail = self.api(source, f"merge_requests/{m.get('iid')}")
            if not isinstance(detail, dict):
                return {'availability': 'unknown (MR detail unavailable)'}
            sha = (detail.get('diff_refs') or {}).get('head_sha') or detail.get('sha')
            if sha == head:
                exact.append({'project': source['project'], 'iid': m['iid'],
                              'url': detail.get('web_url'), 'source_head': sha,
                              'state': detail.get('state'), 'draft': detail.get('draft', False)})
        return _single(exact, matches)


class GitHub(Forge):
    label = 'GitHub'
    binary = 'gh'

    def api(self, identity, endpoint):
        args = ['gh', 'api', '--hostname', identity['host'], f"repos/{identity['project']}/{endpoint}"]
        return self.call(args, (identity['host'], identity['project'], endpoint))

    @staticmethod
    def state(pull):
        if pull.get('state') == 'open':
            return 'opened'
        if pull.get('state') == 'closed':
            return 'merged' if pull.get('merged_at') else 'closed'
        return None

    def change(self, source, branch, head):
        owner = source['project'].split('/')[0]
        matches = []
        for page in range(1, 6):
            data = self.api(source, 'pulls?state=all&head=' + quote(owner + ':' + branch, safe='')
                            + '&base=' + quote(source['target'], safe='') + f'&per_page=100&page={page}')
            if not isinstance(data, list):
                return {'availability': 'unknown (GitHub unavailable)'}
            matches += [p for p in data if isinstance(p, dict)
                        and (p.get('head') or {}).get('ref') == branch
                        and (p.get('base') or {}).get('ref') == source['target']]
            if len(data) < 100:
                break
        else:
            return {'availability': 'unknown (pagination limit)'}
        exact = []
        for pull in matches:
            same_repo = all(((pull.get(side) or {}).get('repo') or {}).get('full_name') == source['project']
                            for side in ('head', 'base'))
            state = self.state(pull)
            if same_repo and state and pull['head'].get('sha') == head and type(pull.get('number')) is int:
                exact.append({'project': source['project'], 'iid': pull['number'],
                              'url': pull.get('html_url'), 'source_head': head,
                              'state': state, 'draft': bool(pull.get('draft'))})
        return _single(exact, matches)


def for_identity(setting, identity, deadline_left):
    """Choose an adapter. `auto` recognizes only hosts that name the forge; otherwise evidence stays
    unknown, which keeps lanes rather than guessing an API."""
    if setting == 'none' or not identity:
        return None
    host = identity['host'].lower()
    if setting == 'github' or setting == 'auto' and 'github' in host:
        return GitHub(deadline_left)
    if setting == 'gitlab' or setting == 'auto' and 'gitlab' in host:
        return GitLab(deadline_left)
    return None
