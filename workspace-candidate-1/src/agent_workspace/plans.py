"""One parser for plan Status fields and the plan index consumers."""
from dataclasses import dataclass
from pathlib import Path
import re


@dataclass
class Plan:
    path: str
    title: str
    fields: dict


def status_block(text):
    match = re.search(r'^## Status\s*$(.*?)(?=^### |^## |\Z)', text, re.S | re.M)
    return match[1].strip() if match else None


def field(block, name):
    match = re.search(rf'\*\*{re.escape(name)}:\*\*\s*(.*?)(?=\n\*\*|\Z|·)', block or '', re.S)
    return ' '.join(match[1].split()) if match else '?'


def read_plan(root, relative):
    root, relative = Path(root), Path(relative)
    path = root / relative
    if (relative.is_absolute() or '..' in relative.parts or not relative.parts
            or relative.parts[0] != 'plans' or path.resolve() != path or path.suffix != '.md'):
        raise ValueError('Plan must be contained in plans/')
    text = path.read_text()
    title = next((line[2:] for line in text.splitlines() if line.startswith('# ')), path.stem)
    return Plan(str(relative), title, {k: field(status_block(text), k) for k in
                ('State', 'Updated', 'Done', 'In progress', 'Next step', 'Branches / MRs')})


def list_plans(root):
    directory = Path(root) / 'plans'
    if not directory.is_dir() or directory.is_symlink():
        return []
    paths = [p for p in directory.glob('*.md') if p.name not in ('README.md', '_template.md')]
    paths += [p / 'README.md' for p in directory.iterdir() if p.is_dir() and not p.is_symlink()]
    result = []
    for path in sorted(paths):
        if path.is_file():
            result.append(read_plan(root, path.relative_to(root)))
    return result
