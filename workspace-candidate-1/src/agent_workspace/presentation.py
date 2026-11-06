"""Terminal and machine views. Rendering cannot change runtime state."""
from collections import Counter
from datetime import datetime
import json
import os
import re
import shutil
import textwrap


def clean(text):
    # Strip OSC/CSI and all remaining controls, including bidi direction controls.
    text = re.sub(r'\x1b\][^\x07]*(?:\x07|\x1b\\)', '', str(text))
    text = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', text)
    return ''.join(c for c in text if c.isprintable() and not '\u202a' <= c <= '\u202e' and not '\u2066' <= c <= '\u2069')


def is_quiet(effort):
    """Nothing for the owner to do: finished, or merged with no follow-up."""
    return effort['quiet']


def status_label(effort):
    return 'Done' if is_quiet(effort) else effort['attention']


def local_time(stamp):
    try:
        return datetime.fromisoformat(stamp).astimezone().strftime('%Y-%m-%d %H:%M %Z')
    except ValueError:
        return f'{stamp[:16]} UTC'


def brief(text, limit=170):
    """Shorten at a sentence or clause boundary; returns (text, was_shortened)."""
    text = ' '.join(str(text).split())
    if len(text) <= limit:
        return text, False
    out = ''
    for part in re.split(r'(?<=\.)\s+(?=[A-Z`])|(?<=;)\s+', text):
        if len(f'{out} {part}'.strip()) > limit:
            break
        out = f'{out} {part}'.strip()
    if out:
        return out.rstrip(';') + ' …', True
    return text[:limit - 1].rsplit(' ', 1)[0].rstrip(',;:') + ' …', True


class Terminal:
    def __init__(self, stream, width=None, title='Workspace'):
        self.title = title
        self.stream = stream
        self.width = max(30, width or shutil.get_terminal_size((90, 24)).columns)
        self.color = stream.isatty() and 'NO_COLOR' not in os.environ and os.environ.get('TERM') != 'dumb'

    def line(self, text='', color=None, hang=0):
        text = clean(text)
        for line in textwrap.wrap(text, self.width, replace_whitespace=False, subsequent_indent=' ' * hang) or ['']:
            print(f'\033[{color}m{line}\033[0m' if color and self.color else line, file=self.stream)

    def recap(self, report, verbose=False):
        self.line(f'{self.title} · Welcome back', '1;36')
        self.line(f"{report['scope']} · {local_time(report['collected_at'])}")
        counts = Counter(status_label(x) for x in report['efforts'])
        self.line(' · '.join(f'{v} {k.lower()}' for k, v in counts.items()) or 'No recorded work needs attention.')
        order = {'In session': 0, 'Continue': 1, 'Review': 2, 'Verify': 3, 'Your action': 3, 'Needs triage': 4, 'Done': 5}
        entries = sorted(report['efforts'], key=lambda x: (order[x['attention']], x['title']))
        needs_you = [e['title'] for e in entries if e['attention'] in ('Your action', 'Review')]
        if needs_you:
            self.line()
            self.line('Do this first: ' + ' · '.join(needs_you), '1;33')
        self.line()
        shortened = False
        triage = []
        for e in entries:
            if not verbose and is_quiet(e):
                continue
            if not verbose and e['attention'] == 'Needs triage':
                triage.append(e)
                continue
            self.line(f"{status_label(e).upper()} · {e['title']} · lane {','.join(map(str, e['lanes'])) or '—'}",
                      '36' if e['attention'] in ('In session', 'Continue') else '32' if is_quiet(e) else '33')
            self.line(e['summary'])
            next_step, cut = (e['next_step'], False) if verbose else brief(e['next_step'])
            shortened |= cut
            self.line('Next    ' + next_step, hang=8)
            if e.get('resume'):
                self.line('Resume  ' + e['resume'], hang=8)
            if e.get('command'):
                self.line('Run     ' + e['command'], hang=8)
            if verbose:
                self.line(f"State   session {e['session']}; code {e['code']}; cleanup {e['cleanup']}")
                self.line('Source  ' + e['source'])
            elif '/plans/' in e['source']:
                self.line('Source  plans/' + e['source'].split('/plans/', 1)[1])
            for warning in e['warnings']:
                self.line('Check   ' + warning, '33', hang=8)
            self.line()
        groups = {}
        for e in triage:
            # A branch and the worktree holding it share a basename; show them as one item.
            groups.setdefault(e['title'].rsplit('/', 1)[-1], []).append(e)
        if groups:
            self.line(f'NEEDS TRIAGE · {len(groups)} items', '33')
            for group in groups.values():
                self.line('  • ' + ' + '.join(e['title'] for e in group), hang=4)
                self.line('    ' + ' '.join(dict.fromkeys(e['summary'] for e in group)), hang=4)
                for e in group:
                    for warning in e['warnings']:
                        self.line('    Check: ' + warning, '33', hang=4)
                    if e.get('command'):
                        self.line('    → ' + e['command'], hang=6)
            self.line()
        done = [e['title'] for e in entries if is_quiet(e)]
        if done and not verbose:
            self.line(f"Done ({len(done)}): " + '; '.join(done), '32')
        if shortened:
            self.line('Some steps are shortened; add --verbose for the full text.')
        for warning in report['warnings']:
            self.line('Check: ' + warning, '33')

    def sweep(self, report, verbose=False):
        self.line(f"{self.title} · " + ('Cleanup preview' if not report.get('applied') else 'Cleanup results'), '1;36')
        self.line(' · '.join(f'{n} {state}' for state, n in Counter(e['class'] for e in report['entries']).items()))
        for e in report['entries']:
            if not verbose and e['kind'] != 'lane' and e['class'] == 'Protected':
                continue
            name = f"lane {e['lane']}" if 'lane' in e else e.get('branch') or e.get('path') or e.get('identity', '?')
            self.line(f"{name}: {e['class']} · {e.get('result') or e['action']}")
            if e.get('protection'):
                self.line('  Retained: ' + '; '.join(e['protection']))
            if e.get('error'):
                self.line('  Check: ' + e['error'], '33')
            if verbose:
                for r in e.get('repos', []):
                    self.line(f"  {r['identity']}: {r.get('branch')} {r.get('head')} · proof {r.get('merge_proof')} · {r.get('push')}")
        for error in report.get('errors', []):
            self.line(error, '31')


def compact_sweep(report, verbose=False):
    if verbose:
        return dict(schema_version=1, **report)
    keys = ('kind', 'lane', 'identity', 'branch', 'path', 'class', 'action', 'protection', 'error', 'recovery', 'result')
    entries = [e for e in report['entries'] if e['kind'] == 'lane' or e['class'] != 'Protected']
    return dict(schema_version=1, entries=[{k: e[k] for k in keys if k in e} for e in entries],
                omitted_protected=len(report['entries'])-len(entries),
                errors=report.get('errors', []), applied=report.get('applied', False))


def emit_json(report, stream):
    print(json.dumps(report, ensure_ascii=False, separators=(',', ':')), file=stream)
