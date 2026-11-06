"""Path guardrail for harness tool calls, not a shell sandbox. Never evaluates shell input."""
import os
from pathlib import Path
import shlex

from .identity import command_prefix


def within(path, parent):
    return path == parent or parent in path.parents


def bootstrap_or_lane_command(tokens, root, tools=('aw',)):
    """A mapped session still runs in the owner's directory. Allow only the attach
    bootstrap, or a command that first enters the lane, so nothing else resolves
    relative paths or runs git against the owner's checkout."""
    if tokens[1:2] == ["attach"] and tokens[0].removeprefix("./") in {"bin/" + name for name in tools}:
        if any(set(t) <= set(";&|()<>") for t in tokens):
            return "run bin/aw attach on its own"
        return None
    if len(tokens) > 2 and tokens[0] == "cd" and tokens[2] in ("&&", ";"):
        target = Path(os.path.expanduser(tokens[1]))
        if target.is_absolute() and within(target.resolve(), root):
            return None
    return f"this session is attached to the lane at {root}: start the command with `{command_prefix(root)}`"


def _violation(payload, root, state, main=None, mapped=False, tools=('aw',)):
    # Protected: the owner's workspace, which contains every lane. Paths elsewhere,
    # such as /tmp or ~/.claude, stay usable.
    root, state = Path(root).resolve(), Path(state).resolve()
    main = Path(main).resolve() if main else root.parent.parent
    protected = (main,)

    def outside(path):
        if within(path, root) or within(path, state):
            return False
        return any(within(path, p) for p in protected)

    cwd = Path(payload.get("cwd", str(root))).resolve()
    tool, args = payload.get("tool_name"), payload.get("tool_input", {})
    # Mapped: a session attached from the owner's checkout, whose own cwd is not the lane.
    bootstrap = mapped and not (within(cwd, root) or within(cwd, state))
    if bootstrap and tool != "Bash":
        given = [args[k] for k in ("file_path", "notebook_path", "path") if args.get(k)]
        given += [e["file_path"] for e in args.get("edits", []) if e.get("file_path")]
        if tool in ("Grep", "Glob") and not args.get("path"):
            return f"this session is attached to the lane at {root}: pass an explicit path inside it"
        if any(not Path(os.path.expanduser(g)).is_absolute() for g in given):
            return f"this session is attached to the lane at {root}: use absolute paths inside it"
        cwd = root
    elif not bootstrap and outside(cwd):
        return f"working directory outside lane: {cwd}"

    def check(value, base=cwd):
        value = value.replace("${HOME}", str(Path.home())).replace("$HOME", str(Path.home()))
        value = os.path.expanduser(value)
        path = (base / value).resolve()
        if outside(path):
            return f"path outside lane: {path}"

    if tool != "Bash":
        paths = [args[k] for k in ("file_path", "notebook_path", "path") if args.get(k)]
        pattern = args.get("pattern", "") if tool == "Glob" else ""
        if pattern.startswith(("/", "~", "$HOME", "${HOME}")):
            paths.append(pattern.split("*", 1)[0] or "/")
        paths += [e["file_path"] for e in args.get("edits", []) if e.get("file_path")]
        for path in paths:
            if found := check(path):
                return found
        return None

    command = args.get("command", "")
    try:
        lexer = shlex.shlex(command, posix=True, punctuation_chars=";&|()<>")
        lexer.whitespace_split = True
        tokens = list(lexer)
    except ValueError:
        return "cannot safely parse shell quoting"
    base = cwd
    if bootstrap:
        if reason := bootstrap_or_lane_command(tokens, root, tools):
            return reason
        if tokens[0] != "cd":
            return None  # the attach bootstrap, which takes no paths
        base = root
    for i, token in enumerate(tokens):
        # Literal paths only: computed shell expressions are not a sandbox boundary.
        candidate = token.split("=", 1)[-1] if "=" in token else token
        if "://" in candidate:
            continue
        if candidate in (".", "..") or candidate.startswith(("/", "./", "../", "~", "$HOME", "${HOME}")) or "/" in candidate:
            if found := check(candidate, base):
                return found
        if i and tokens[i - 1] == "cd" and token not in ("-", "--"):
            if found := check(token, base):
                return found
            base = (base / token).resolve()
    return None


def violation(payload, root, state, main=None, mapped=False, tools=('aw',)):
    reason = _violation(payload, root, state, main, mapped, tools)
    if reason:
        return (reason + f' Assigned lane: {root}. Start shell commands with `{command_prefix(root)}`; '
                'use absolute paths inside the assigned lane for file tools. '
                'Inspect bin/aw whoami --json if the path is uncertain.')
    return None
