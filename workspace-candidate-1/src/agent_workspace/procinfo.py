"""Process identity that survives PID reuse, on Linux and on macOS.

An identity is a string that stays constant for one process and differs for a later process that
reuses its PID. Identities are only compared with each other, so the two sources never mix:
a record written on one machine is never read on another. Failure returns None, which callers
treat as "process gone or unknown" according to their own conservative rule.
"""
from pathlib import Path
import subprocess


def _ps(pid, field):
    try:
        result = subprocess.run(['ps', '-o', f'{field}=', '-p', str(int(pid))], capture_output=True,
                                text=True, timeout=5, env={'LC_ALL': 'C', 'PATH': '/bin:/usr/bin'})
    except (OSError, ValueError, subprocess.SubprocessError):
        return None
    text = ' '.join(result.stdout.split())
    return text if result.returncode == 0 and text else None


def _stat(pid):
    return Path(f'/proc/{int(pid)}/stat').read_text().rsplit(')', 1)[1].split()


def start_identity(pid):
    """Process start time: clock ticks from /proc, or the lstart text from ps."""
    try:
        return _stat(pid)[19]
    except FileNotFoundError:
        pass
    except (OSError, ValueError, IndexError):
        return None
    if Path('/proc/self/stat').exists():
        return None  # /proc works here, so the process is gone.
    return _ps(pid, 'lstart')


def parent(pid):
    try:
        return int(_stat(pid)[1])
    except FileNotFoundError:
        pass
    except (OSError, ValueError, IndexError):
        return None
    if Path('/proc/self/stat').exists():
        return None
    text = _ps(pid, 'ppid')
    return int(text) if text and text.isdigit() else None


def name(pid):
    try:
        return Path(f'/proc/{int(pid)}/comm').read_text().strip()
    except FileNotFoundError:
        pass
    except (OSError, ValueError):
        return None
    if Path('/proc/self/stat').exists():
        return None
    text = _ps(pid, 'comm')
    return Path(text).name if text else None
