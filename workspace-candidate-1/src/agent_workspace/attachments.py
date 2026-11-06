"""Attachment records: which session owns which lane when no launcher holds session.lock.

Records are JSON files in STATE/attachments, written atomically and never sourced by a shell.
They hold a lane number and generation, never paths, so a relocation keeps them valid.
"""
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import subprocess
import sys
import time

from .procinfo import names, parent, start_identity

SCHEMA = 1
PROTECTED = ("Attaching", "Attached", "Uncertain")
HARNESSES = ("claude", "grok", "codex")  # harnesses whose hook payloads route to a lane


def path_for(state, harness, session):
    digest = hashlib.sha256(session.encode()).hexdigest()[:32]
    return Path(state) / "attachments" / f"{harness}-{digest}.json"


def load(path):
    try:
        if path.is_symlink():
            raise ValueError('symlink attachment')
        data = json.loads(path.read_text())
        if not isinstance(data, dict) or data.get("schema_version") != SCHEMA or data["state"] not in PROTECTED + ("Ended",):
            raise ValueError
        if type(data['lane']) is not int or data['lane'] < 1:
            raise ValueError('invalid lane')
        procs = data.get('processes', [])
        if not isinstance(procs, list) or any(not isinstance(p, dict) or
                not str(p.get('pid', '')).isdigit() or not isinstance(p.get('start'), str) or not p['start'] for p in procs):
            raise ValueError('invalid process identities')
        return data
    except (OSError, ValueError, KeyError, TypeError):
        # Unreadable or unknown records protect nothing we can name, so fail safe:
        # callers treat a malformed file as an Uncertain record of unknown lane.
        return None


def save(path, data):
    if path.is_symlink():
        raise ValueError('symlink attachment')
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    with tmp.open("w") as handle:
        json.dump(data, handle, indent=1)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(tmp, path)


def bound(data):
    """The processes that hold the session: the one that attached, plus any that joined."""
    procs = data.get("processes")
    if procs is None and data.get("pid"):
        procs = [{"pid": data["pid"], "start": data.get("pid_start")}]
    return procs or []


def refreshed(path, data):
    """Apply the one liveness rule we can verify: every bound process identity is gone.

    A resumed conversation keeps its session id but runs in a new process, so the record
    ends only when none of the processes that joined it is alive."""
    procs = bound(data)
    if data["state"] in ("Attaching", "Attached", "Uncertain") and procs:
        if not any(start_identity(p["pid"]) == p.get("start") for p in procs):
            data["state"] = "Ended"
            data["ended_reason"] = "process exited"
            data["ended_at"] = int(time.time())
            save(path, data)
    return data


def records(state):
    for path in sorted((Path(state) / "attachments").glob("*.json")):
        data = load(path)
        if data is None:
            yield path, {"lane": None, "state": "Uncertain", "malformed": True}
        else:
            yield path, refreshed(path, data)


def protecting(state, lane):
    for path, data in records(state):
        if data["state"] in PROTECTED and (data["lane"] is None or str(data["lane"]) == str(lane)):
            return path, data
    return None


def begin(state, harness, session, lane, generation, pid=None):
    path = path_for(state, harness, session)
    now = int(time.time())
    save(path, {
        "schema_version": SCHEMA, "harness": harness, "session": session, "lane": int(lane),
        "generation": generation, "state": "Attaching", "pid": pid or None,
        "pid_start": start_identity(pid) if pid else None,
        "processes": [{"pid": int(pid), "start": start_identity(pid)}] if pid else [],
        "token": secrets.token_hex(16), "created": now, "last_contact": now,
    })


def cmd_set(state, harness, session, lane, generation, new):
    path = path_for(state, harness, session)
    data = load(path)
    if not data or data["generation"] != generation or str(data["lane"]) != str(lane):
        sys.exit("attachment record does not match this lane generation")
    data["state"], data["last_contact"] = new, int(time.time())
    save(path, data)


def harness_process(harness):
    """PID of the nearest ancestor of this hook that is the harness itself, or None.

    Hooks run as children of the harness (outside Codex's sandbox), so their ancestry names
    the live process even where a tool command cannot see it.

    AW_HARNESS_ANCESTRY_STOP=<pid> ends the search at that process, so a test run can ignore a
    harness above the test runner."""
    try:
        stop = int(os.environ.get('AW_HARNESS_ANCESTRY_STOP') or 1)
        pid = os.getppid()
        while pid and pid > 1 and pid != stop:
            if harness in names(pid):
                return pid
            pid = parent(pid)
    except (OSError, ValueError):
        pass
    return None


def adopt(path, data):
    """Register the harness process that is running this hook.

    This covers a resumed session that never re-ran bin/aw attach."""
    pid = harness_process(data.get("harness"))
    if pid is None:
        return
    start = start_identity(pid)
    procs = [p for p in bound(data) if start_identity(p["pid"]) == p.get("start")]
    if start is None or any(p["pid"] == pid and p["start"] == start for p in procs):
        return
    data["processes"], data["last_contact"] = procs + [{"pid": pid, "start": start}], int(time.time())
    save(path, data)


def cmd_join(state, harness, session, generation, pid):
    """Add a live process to a protected record, so a resumed session keeps its lane."""
    path = path_for(state, harness, session)
    data = load(path)
    if not data or data["generation"] != generation or data["state"] not in PROTECTED:
        sys.exit("no protecting attachment record for this session")
    start = start_identity(pid)
    if start is None:
        sys.exit("process identity could not be verified")
    procs = [p for p in bound(data) if start_identity(p["pid"]) == p.get("start")]
    if not any(p["pid"] == int(pid) and p["start"] == start for p in procs):
        procs.append({"pid": int(pid), "start": start})
        print(f"Registered process {pid} for this session; the lane stays reserved while any "
              "registered process runs.", file=sys.stderr)
    data["processes"], data["last_contact"] = procs, int(time.time())
    save(path, data)


def revive(state, harness, session, lanes, pid=None):
    """Return an Ended session to its old lane, if nothing has claimed the lane since.

    Returns the lane number or None. The generation file changes whenever the lane is allocated
    to anyone, so a matching generation and no protecting record mean the lane is still the
    session's. The caller holds the state lock."""
    path = path_for(state, harness, session)
    data = load(path)
    if not data or data["state"] != "Ended":
        return None
    lane_state = Path(state) / "lanes" / str(int(data["lane"]))
    try:
        current = (lane_state / "generation").read_text().strip()
    except OSError:
        return None
    if (current != data["generation"] or (lane_state / "claimed").exists()
            or not (Path(lanes) / str(int(data["lane"])) / ".lane").is_file()
            or protecting(state, data["lane"])):
        return None
    start = start_identity(pid) if pid else None
    data.update(state="Attached", processes=[{"pid": int(pid), "start": start}] if start else [],
                last_contact=int(time.time()))
    for key in ("ended_reason", "ended_at"):
        data.pop(key, None)
    save(path, data)
    return data["lane"]


def revive_locked(state, harness, session, lanes):
    """The hook's revive: a resumed session whose record ended is confined again at once.

    Takes the same state lock as allocation, so it cannot interleave with a claim."""
    import fcntl
    pid = harness_process(harness)
    if pid is None:
        return None  # without a live process to track, the record could never end again
    try:
        with open(Path(state) / "lock", "a") as handle:
            fcntl.flock(handle, fcntl.LOCK_EX)
            return revive(state, harness, session, lanes, pid)
    except OSError:
        return None


def describe(state, harness, session, root, context):
    path = path_for(state, harness, session)
    data = load(path)
    if not data:
        sys.exit("no attachment record")
    root = Path(root)

    def git(repo, *args):
        out = subprocess.run(["git", "-C", str(repo), *args], text=True, capture_output=True)
        return out.stdout.strip() if out.returncode == 0 else None

    repos = {"workspace": root}
    for directory, alias in context.nested_projects:
        repos[alias] = root / directory
    lane_state = Path(state) / "lanes" / str(data["lane"])
    if (lane_state / "setup.ok").exists():
        setup = "ready"
    elif (lane_state / "setup.lock").exists():
        setup = "running"
    else:
        setup = "unknown"
    return {
        "schema_version": SCHEMA,
        "session": {"harness": harness, "id": session},
        "lane": data["lane"],
        "paths": {name: str(p) for name, p in repos.items()},
        "branches": {name: git(p, "branch", "--show-current") for name, p in repos.items()},
        "base_shas": {name: git(p, "rev-parse", "HEAD") for name, p in repos.items()},
        "reservation": {"state": data["state"], "generation": data["generation"]},
        "setup": {"status": setup, "log": str(lane_state / "setup.log")},
        "next": f"Start commands with `cd {shlex.quote(str(root))} &&` and use absolute lane paths for "
                f"edits. Read {root}/AGENTS.md. Run project commands with {root}/bin/aw run. "
                "Inspect slots with bin/aw slots; run bin/aw serve in the background when you need a server.",
    }


