"""Optional plan tooling: plan status, convention checks and a pre-commit reminder.

A workspace with no plans/ directory passes every check, so adopting lanes never requires
adopting plans. Conventions are described in docs/plans.md.
"""
import datetime
import os
from pathlib import Path
import re
import subprocess
import sys

from .plans import field, list_plans, status_block

STATES = {'DRAFT', 'PLANNED', 'PARTIALLY IMPLEMENTED', 'FULLY IMPLEMENTED', 'FULLY VERIFIED'}
FIELDS = ['State', 'Updated', 'Branches / MRs', 'Done', 'In progress', 'Next step']
STALE_DAYS = 14
LINK = re.compile(r'\[[^\]]*\]\(([^)\s]+)\)')


def git(repo, *args):
    try:
        return subprocess.run(['git', '-C', str(repo), *args], capture_output=True, text=True,
                              timeout=10).stdout.strip()
    except (OSError, subprocess.SubprocessError):
        return ''


def plan_status(context, slug=None):
    plans = list_plans(context.root)
    if slug:
        for plan in plans:
            if Path(plan.path).stem == slug or Path(plan.path).parent.name == slug:
                print(status_block((context.root / plan.path).read_text()))
                return 0
        print(f'No plan named {slug}', file=sys.stderr)
        return 1
    today = datetime.date.today()
    for plan in plans:
        state, updated = plan.fields['State'], plan.fields['Updated']
        note = ''
        try:
            age = (today - datetime.date.fromisoformat(updated)).days
            if age > STALE_DAYS and state in ('PARTIALLY IMPLEMENTED', 'FULLY IMPLEMENTED'):
                note = f'  [Status is {age} days old: verify it]'
        except ValueError:
            pass
        print(f"{Path(plan.path).stem}\n  {state} · updated {updated}{note}\n  next: {plan.fields['Next step']}")
    print('\nRepo state (check Status against this before you rely on it):')
    for label, repo in (('workspace', context.root), *((a, context.root / d) for d, a in context.nested_projects)):
        if not (repo / '.git').exists():
            print(f'  {label}: not cloned')
            continue
        branch = git(repo, 'branch', '--show-current') or '(detached)'
        dirty = 'uncommitted changes' if git(repo, 'status', '--porcelain') else 'clean'
        print(f'  {label}: {branch}, {dirty}')
    return 0


def plan_files(root, folders, problems):
    plans = root / 'plans'
    found = {}
    for path in sorted(plans.iterdir()):
        if path.name in ('README.md', '_template.md') or path.name.startswith('.'):
            continue
        if path.is_file() and path.suffix == '.md':
            found[path.name] = path
        elif path.is_dir():
            if path.name not in folders:
                problems.append(f'plans/{path.name}/: plan folders are only for {sorted(folders)}; '
                                f'make it plans/{path.name}.md or add it to docs.plan_folders')
            if (path / 'README.md').is_file():
                found[f'{path.name}/README.md'] = path / 'README.md'
            else:
                problems.append(f'plans/{path.name}/: missing README.md')
        else:
            problems.append(f'plans/{path.name}: unexpected file; plans are markdown')
    return found


def check_status(relative, path, problems):
    text = path.read_text(encoding='utf-8')
    match = re.search(r'^## Status\s*$(.*?)(?=^## |\Z)', text, re.S | re.M)
    if not match:
        problems.append(f"plans/{relative}: no '## Status' section")
        return None
    body = match[1]
    for name in FIELDS:
        if not re.search(rf'\*\*{re.escape(name)}:\*\*', body):
            problems.append(f'plans/{relative}: Status block is missing **{name}:**')
    state_match = re.search(r'\*\*State:\*\*\s*([A-Z ]+?)\s*(?:·|$)', body, re.M)
    state = state_match[1].strip() if state_match else None
    if state not in STATES:
        problems.append(f'plans/{relative}: invalid State {state!r}; use one of {sorted(STATES)}')
    if not re.search(r'\*\*Updated:\*\*\s*\d{4}-\d{2}-\d{2}', body):
        problems.append(f'plans/{relative}: Status needs an absolute **Updated:** YYYY-MM-DD date')
    return state


def check_index(root, files, problems):
    rows = {}
    index = root / 'plans/README.md'
    if not index.is_file():
        if files:
            problems.append('plans/README.md: missing plan index')
        return rows
    for match in re.finditer(r'^\|\s*\[[^\]]+\]\(([^)]+)\)\s*\|\s*([A-Z ]+?)\s*\|', index.read_text(encoding='utf-8'), re.M):
        rows[match[1]] = match[2]
    for relative in files:
        if relative not in rows:
            problems.append(f'plans/{relative}: not listed in plans/README.md')
    for link in rows:
        if link not in files:
            problems.append(f'plans/README.md: row links to {link}, which is not a plan')
    return rows


def check_links(context, problems):
    root = context.root
    nested = {d for d, _ in context.nested_projects}
    outside = nested | set(context.manifest.ignored_link_prefixes)
    skip = {'.git', context.lanes.name, '.worktrees', 'node_modules', *outside}
    for directory, names, files in os.walk(root):
        names[:] = [n for n in names if n not in skip]
        for name in files:
            if not name.endswith('.md'):
                continue
            path = Path(directory) / name
            relative = path.relative_to(root)
            if name == 'STATUS.md':
                problems.append(f"{relative}: STATUS.md is not allowed; keep status in the plan's ## Status block")
            fenced = False
            for number, line in enumerate(path.read_text(encoding='utf-8').splitlines(), 1):
                if line.lstrip().startswith('```'):
                    fenced = not fenced
                if fenced:
                    continue
                for target in LINK.findall(re.sub(r'`[^`]*`', '', line)):
                    part = target.split('#')[0]
                    if re.match(r'^[a-z]+:', target) or not part:
                        continue
                    resolved = Path(os.path.normpath(Path(directory) / part))
                    # Project repos are separate checkouts and may be absent, as in CI.
                    if os.path.relpath(resolved, root).split(os.sep)[0] in outside:
                        continue
                    if not resolved.exists():
                        problems.append(f'{relative}:{number}: broken link {target}')


def check(context):
    root, problems = context.root, []
    files = {}
    if (root / 'plans').is_dir():
        files = plan_files(root, set(context.manifest.plan_folders), problems)
        states = {relative: check_status(relative, path, problems) for relative, path in files.items()}
        rows = check_index(root, files, problems)
        for relative, state in states.items():
            if state and relative in rows and rows[relative] != state:
                problems.append(f'plans/{relative}: Status says {state} but plans/README.md says {rows[relative]}')
    check_links(context, problems)
    if problems:
        print('Documentation check failed:', file=sys.stderr)
        for problem in problems:
            print('  - ' + problem, file=sys.stderr)
        return 1
    print(f'Documentation check passed ({len(files)} plans).')
    return 0


def plan_reminder(context, repo):
    """Warn, never fail, when a plan listing this branch has not changed since the last commit."""
    branch = git(repo, 'branch', '--show-current')
    if not branch or branch in ('main', 'master'):
        return 0
    head = git(repo, 'log', '-1', '--format=%ct')
    last_commit = int(head) if head.isdigit() else 0
    for plan in list_plans(context.root):
        path = context.root / plan.path
        if f'`{branch}`' in plan.fields['Branches / MRs'] and path.stat().st_mtime < last_commit:
            print(f"Reminder: plan {plan.path} lists branch '{branch}' but its Status has not been "
                  'updated since the last commit.', file=sys.stderr)
    return 0


__all__ = ['check', 'field', 'plan_reminder', 'plan_status']
