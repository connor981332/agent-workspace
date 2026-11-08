#!/bin/sh
# Records a harness's hook payloads, for the adapter fixtures in this directory
# (architecture.md, "Harness adapters": payload formats are not stable, so each adapter is tested
# against recorded payloads).
#
#   sh record.sh setup <harness>      make a throwaway repository with a recording hook
#   (run the harness there yourself, as it prints, and approve its hook or folder trust)
#   sh record.sh collect <harness>    copy the recorded payloads here, with paths and ids replaced
#
# The recording hook saves each payload and allows the call; it changes nothing else. The
# throwaway repository is under your home directory, because Grok loads no project hooks in a
# shared temporary directory. Remove it afterwards with: rm -rf ~/aw-payload-recording
set -eu
here=$(cd "$(dirname "$0")" && pwd)
box=$HOME/aw-payload-recording
[ $# -eq 2 ] || { echo "usage: sh record.sh setup|collect claude|codex|grok|opencode" >&2; exit 2; }
action=$1 harness=$2

prompt='Do exactly these steps with your tools, and nothing else: 1. Read the file notes.txt. 2. Create a file out.txt containing hi. 3. Edit out.txt to replace hi with hello. 4. Run the shell command: ls -la && git status --short. 5. Search the files for hello. 6. List the *.txt files. Then reply done.'

case $action in
setup)
	rm -rf "$box"
	mkdir -p "$box/.recorded"
	cd "$box"
	git init -q
	printf 'line one\nline two\n' > notes.txt
	printf '.recorded/\n.codex/\n.grok/\n.claude/\n.opencode/\n' > .gitignore
	git add notes.txt .gitignore
	git -c user.name=recorder -c user.email=recorder@example.invalid commit -qm init
	cat > "$box/.recorded/rec.sh" <<EOF
#!/bin/sh
n=\$(ls "$box/.recorded" | grep -c json || true)
cat > "$box/.recorded/\$(printf %03d "\$n")-\$1.json"
exit 0
EOF
	chmod +x "$box/.recorded/rec.sh"
	hooks=$(python3 - "$box" "$harness" <<'PY'
import json, sys
box, harness = sys.argv[1:]
events = {"SessionStart": "session-start", "UserPromptSubmit": "user-prompt-submit", "PreToolUse": "pre-tool-use",
          "PostToolUse": "post-tool-use", "Stop": "stop", "SessionEnd": "session-end"}
print(json.dumps({"hooks": {e: [{"hooks": [{"type": "command", "command": f"{box}/.recorded/rec.sh {name}"}]}]
                            for e, name in events.items()}}, indent=1))
PY
)
	case $harness in
	claude) mkdir -p .claude && printf '%s\n' "$hooks" > .claude/settings.json ;;
	codex) mkdir -p .codex && printf '%s\n' "$hooks" > .codex/hooks.json ;;
	grok) mkdir -p .grok/hooks && printf '%s\n' "$hooks" > .grok/hooks/record.json ;;
	*) echo "record.sh: no recording setup for $harness yet" >&2; exit 2 ;;
	esac
	echo "Ready. Now run, in another terminal:"
	echo
	echo "  cd $box && $harness"
	echo
	echo "Approve the hooks or folder trust when it asks (Codex: trust the hooks; Grok: /hooks-trust),"
	echo "approve each tool call, and give it this prompt:"
	echo
	echo "  $prompt"
	echo
	echo "Then quit the harness, and run: sh $0 collect $harness"
	;;
collect)
	version=$("$harness" --version 2>/dev/null | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
	dst=$here/$harness-$version
	mkdir -p "$dst"
	python3 - "$box" "$dst" "$HOME" <<'PY'
import glob, json, os, sys
box, dst, home = sys.argv[1:]
files = sorted(glob.glob(f"{box}/.recorded/*.json"))
if not files:
    sys.exit("no payloads were recorded: check that the harness ran its hooks")
for f in files:
    event = os.path.basename(f)[4:-5]
    text = open(f).read().replace(box, "/WORKSPACE").replace(home, "/HOME")
    data = json.loads(text)
    tool = data.get("tool_name") or data.get("toolName") or ""
    for key in ("session_id", "sessionId", "prompt_id", "turn_id"):
        if isinstance(data.get(key), str):
            text = text.replace(data[key], key.upper())
    data = json.loads(text)
    for key in ("tool_response", "toolResponse", "tool_output"):
        if key in data:
            data[key] = "(omitted)"
    name = event + ("-" + tool.lower().replace("_", "-") if tool else "")
    out = f"{dst}/{name}.json"
    i = 2
    while os.path.exists(out):
        out = f"{dst}/{name}-{i}.json"
        i += 1
    json.dump(data, open(out, "w"), indent=1, sort_keys=True)
print(f"wrote {len(files)} payloads to {dst}")
PY
	;;
*) echo "usage: sh record.sh setup|collect <harness>" >&2; exit 2 ;;
esac
