#!/usr/bin/env python3
"""Normalize a harness hook payload to the Claude Code shape that the hooks read.

Claude Code payloads pass through unchanged. Codex already sends that shape, except that it
edits files with apply_patch, whose patch text names the files. Grok sends camelCase fields and its own tool
names; they are translated here so the guard has one input format:

  hookEventName/sessionId/toolName/toolInput  ->  session_id/tool_name/tool_input

Tool names and argument names were measured against Grok 1.0.67 (plans/lane-attachment.md).
A tool this table does not know keeps its name, so a path argument is still checked.
"""
import json
import sys

TOOLS = {
    "run_terminal_command": "Bash",
    "read_file": "Read",
    "write": "Write",
    "search_replace": "Edit",
    "grep": "Grep",
}


def patch_files(text):
    """File paths named by a Codex apply_patch body."""
    prefixes = ("*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: ")
    return [line[len(p):].strip() for line in text.splitlines() for p in prefixes if line.startswith(p)]


def normalize(payload):
    if payload.get("tool_name") == "apply_patch":
        # Codex edits files with a patch; treat each named file as an edit target.
        text = (payload.get("tool_input") or {}).get("command", "")
        return {**payload, "tool_name": "MultiEdit",
                "tool_input": {"edits": [{"file_path": f} for f in patch_files(text)]}}
    if "hookEventName" not in payload:
        return payload
    tool_input = dict(payload.get("toolInput") or {})
    if "target_file" in tool_input and "file_path" not in tool_input:
        tool_input["file_path"] = tool_input.pop("target_file")
    out = {k: v for k, v in payload.items() if k not in ("hookEventName", "sessionId", "toolName", "toolInput")}
    out.update(
        hook_event_name=payload["hookEventName"],
        session_id=payload.get("sessionId", ""),
        tool_name=TOOLS.get(payload.get("toolName", ""), payload.get("toolName", "")),
        tool_input=tool_input,
    )
    return out


if __name__ == "__main__":
    print(json.dumps(normalize(json.load(sys.stdin))))
