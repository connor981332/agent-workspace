"""Activation state for one checkout.

Hooks stay in read-only diagnosis until the owner activates a checkout. A workspace that has
not finished bootstrap must not start allocating lanes, because attach would fail half way
through with worktrees already created.
"""

PROTOCOL = 2


def path(context):
    return context.git_file('tooling-version')


def protocol(context):
    target = path(context)
    if not target.exists():
        return 1
    try:
        return int(target.read_text().strip())
    except (ValueError, OSError) as exc:
        raise ValueError(f'Unreadable activation record: {target}') from exc


def health(context):
    return 'active' if protocol(context) == PROTOCOL else 'inactive'


def activate(context):
    path(context).write_text(f'{PROTOCOL}\n')
    context.gate_path.touch()
