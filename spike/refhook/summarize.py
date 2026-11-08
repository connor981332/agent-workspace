#!/usr/bin/env python3
"""Collapse run.sh TSV output (one or more files) into a Markdown matrix: for each command run from
a lane, the refs the hook sees in the states it can reject (preparing, prepared), per Git version."""
import collections, sys
rows = collections.defaultdict(lambda: collections.defaultdict(set))
ctx = collections.defaultdict(set)
versions = []
for path in sys.argv[1:]:
    for line in open(path):
        f = line.rstrip('\n').split('\t')
        if len(f) < 9:
            f += [''] * (9 - len(f))
        ver, where, cmd, state, ref, old, new, cwd, gd = f[:9]
        if ver not in versions:
            versions.append(ver)
        key = (where, cmd)
        if state in ('preparing', 'prepared'):
            label = ref + ('' if old == 'oid' or new == 'oid' else ' (delete)' if new == 'zero' and old == 'zero' else '')
            if new == 'zero':
                label = ref + ' (delete)'
            rows[key][ver].add(f'{state[:4]}:{label}')
            ctx[(where, ver)].add(f'{cwd}/{gd}')
        elif state == '(no hook call)':
            rows[key][ver].add('—')
        else:
            rows[key][ver]  # ensure key exists
print('| From | Command | ' + ' | '.join(versions) + ' |')
print('|---|---|' + '---|' * len(versions))
for (where, cmd), per in rows.items():
    cells = []
    for v in versions:
        refs = sorted(r for r in per.get(v, ()) if r != '—') or (['—'] if per.get(v) else ['(not run)'])
        prep = sorted({r.split(':', 1)[1] for r in refs if r.startswith('prep')} ) or refs
        has_preparing = any(r.startswith('prep') and r.startswith('prep') for r in refs)
        cells.append(', '.join(f'`{r}`' if r not in ('—', '(not run)') else r for r in prep))
    print(f'| {where} | {cmd} | ' + ' | '.join(cells) + ' |')
print()
for (where, ver), c in sorted(ctx.items()):
    print(f'- Git {ver}, commands from the {where}: hook cwd/GIT_DIR seen: {", ".join(sorted(c))}')
