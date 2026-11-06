"""Lane lifecycle, guards and attachment over real Git fixtures with fake external tools."""
import sys
import json
import os
import fcntl
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

ROOT = Path(__file__).resolve().parents[1]
os.environ['AW_TOOL_PATH'] = str(ROOT)
sys.path.insert(0, str(ROOT / 'tests'))


def close_process(process):
    if process.poll() is None:
        process.kill()
    process.wait(timeout=5)
    for stream in (process.stdin, process.stdout, process.stderr):
        if stream:
            stream.close()
sys.path.insert(0, str(ROOT / 'src'))
import fixtures
from agent_workspace import boundary, forge, lifecycle
from agent_workspace.context import Context
from agent_workspace.runtime import Runtime


def run(*args, cwd=None, ok=True, input_text=None):
    result = subprocess.run(args, cwd=cwd, text=True, capture_output=True, input=input_text)
    if ok and result.returncode:
        raise AssertionError(result.stderr + result.stdout)
    return result


class LaneTests(unittest.TestCase):
    def setUp(self):
        # A harness running this suite must not count as the hook's ancestor.
        ignore_ancestry = patch.dict(os.environ, {'AW_HARNESS_ANCESTRY_STOP': str(os.getpid())})
        ignore_ancestry.start()
        self.addCleanup(ignore_ancestry.stop)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name).resolve()  # macOS temp directories sit behind a symlink.
        self.main = self.base / "workspace with spaces"
        self.main.mkdir()

    def init_repo(self, path):
        path.mkdir(exist_ok=True, parents=True)
        run("git", "init", "-q", str(path))
        run("git", "config", "user.email", "fixture@example.invalid", cwd=path)
        run("git", "config", "user.name", "Fixture", cwd=path)
        (path / "file").write_text("initial\n")
        run("git", "add", ".", cwd=path)
        run("git", "commit", "-qm", "fixture", cwd=path)
        if path == self.main:
            (path / ".git/aw-tooling-version").write_text("2\n")

    def test_boundary(self):
        lane = self.main / ".lanes/1"
        lane.mkdir(parents=True)
        state = self.main / ".lanes/.aw/lanes/1"
        (lane / "escape").symlink_to(self.main)
        cases = [
            (f"cat '{lane}/file'", False),
            (f"cat '{lane}/file' '{self.main}/file'", True),
            (f"cat '{self.main}/.lanes/10/file'", True),
            ("cat ../../AGENTS.md", True),
            ("cat escape/AGENTS.md", True),
            ("cd project-api && cat ../AGENTS.md", False),
            ("cd project-api && cat ../../../AGENTS.md", True),
            (f"cat '{state}/api.log'", False),
            (f"cat '{state.parent}/2/api.env'", True),
            ("bin/aw run api npm test -- test/unit/example.ts", False),
            ("cat /tmp/claude-scratch/out.txt ~/.claude/notes.md", False),
            ("echo $(pwd)/x 2>/dev/null", False),
        ]
        for command, blocked in cases:
            with self.subTest(command=command):
                self.assertEqual(bool(boundary.violation({"cwd": str(lane), "tool_name": "Bash", "tool_input": {"command": command}}, lane, state)), blocked)
        for tool in ("Read", "Write", "Grep", "Glob"):
            self.assertTrue(boundary.violation({"tool_name": tool, "tool_input": {"path": str(self.main)}}, lane, state))
        self.assertFalse(boundary.violation({"cwd": str(lane), "tool_name": "Read", "tool_input": {"file_path": "/tmp/tool-output.txt"}}, lane, state))
        glob = lambda pattern: boundary.violation({"cwd": str(lane), "tool_name": "Glob", "tool_input": {"pattern": pattern}}, lane, state)
        self.assertTrue(glob(f"{self.main}/**/*.md"))
        self.assertFalse(glob(f"{lane}/**/*.md"))
        self.assertFalse(glob("**/*.md"))

    def test_guard_hook_fails_closed_and_ignores_the_gate(self):
        self.init_repo(self.main)
        fixtures.write_workspace(self.main)
        run("git", "add", ".", cwd=self.main)
        run("git", "commit", "-qm", "tooling", cwd=self.main)
        lane = self.main / ".lanes/1"
        run("git", "worktree", "add", "-qb", "lane", str(lane), cwd=self.main)
        (lane / ".lane").write_text("AW_LANE=1\n")

        def hook(command):
            payload = json.dumps({"cwd": str(lane), "tool_name": "Bash", "tool_input": {"command": command}})
            return subprocess.run(["python3", str(lane / "bin/aw"), "hook", "pre-tool-use"], input=payload,
                                  text=True, capture_output=True)

        # Lane maintenance holds the exclusive gate: the guard still decides, in both directions.
        holder = subprocess.Popen([sys.executable, "-c", "import fcntl,sys,time; f=open(sys.argv[1],'a'); fcntl.flock(f,fcntl.LOCK_EX); time.sleep(60)", str(self.main / ".git/aw-lanes.lock")])
        self.addCleanup(close_process, holder)
        time.sleep(1)
        self.assertEqual(hook(f"cat '{lane}/file'").returncode, 0)
        self.assertEqual(hook(f"cat '{self.main}/file'").returncode, 2)
        holder.kill()
        holder.wait()
        # A guard that cannot run blocks instead of letting the tool call through.
        (lane / "workspace.toml").write_text("not [valid toml")
        result = hook(f"cat '{lane}/file'")
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("could not run", result.stderr)

    def test_real_hook_and_routing(self):
        self.init_repo(self.main)
        fixtures.write_workspace(self.main)
        run("git", "add", ".", cwd=self.main)
        run("git", "commit", "-qm", "tooling", cwd=self.main)
        lane = self.main / ".lanes/1"
        run("git", "worktree", "add", "-qb", "lane", str(lane), cwd=self.main)
        (lane / ".lane").write_text("AW_LANE=1\n")
        for cmd, code in ((f"cat '{lane}/file'", 0), (f"cat '{self.main}/file'", 2),
                          ("pkill next", 2), ("docker compose down", 2), ("npm run dev", 2)):
            payload = json.dumps({"cwd": str(lane), "tool_name": "Bash", "tool_input": {"command": cmd}})
            result = subprocess.run(["python3", str(lane / "bin/aw"), "hook", "pre-tool-use"], input=payload, text=True, capture_output=True)
            self.assertEqual(result.returncode, code, result.stderr)
        (lane / "project-api").mkdir()
        settings = json.loads((lane / ".claude/settings.json").read_text())
        hook = settings["hooks"]["PreToolUse"][0]["hooks"][0]["command"]
        payload = json.dumps({"cwd": str(lane / "project-api"), "tool_name": "Read", "tool_input": {"file_path": str(self.main / "file")}})
        result = subprocess.run(["sh", "-c", hook], env={**os.environ, "CLAUDE_PROJECT_DIR": str(lane / "project-api")}, input=payload, text=True, capture_output=True)
        self.assertEqual(result.returncode, 2, result.stderr)
        result = subprocess.run(["python3", str(self.main / "bin/aw"), "hook", "pre-tool-use"], input=payload, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        for override in (str(self.base / "outside"), str(self.main / ".git")):
            result = subprocess.run(["python3", str(self.main / "bin/aw"), "ls"], env={**os.environ, "AW_LANES_DIR": override}, text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
        from unittest.mock import patch
        runtime = Runtime(Context.discover(self.main))
        with patch('agent_workspace.runtime.command', return_value=subprocess.CompletedProcess([],0,'/old:/old','')):
            with self.assertRaisesRegex(ValueError, 'stale'):
                runtime.check_mounts()
        self.assertNotEqual(run("python3", str(self.main / "bin/aw"), "park", "../2", ok=False).returncode, 0)
        (lane / ".lane").unlink()
        result = run("python3", str(lane / "bin/aw"), "run", "api", "true", ok=False)
        self.assertIn("refusing owner-container", result.stderr)

    def attach_fixture(self):
        """A workspace with tooling, two project repos and a stub docker, so attach runs for real."""
        self.init_repo(self.main)
        run("git", "branch", "-M", "master", cwd=self.main)
        fixtures.write_workspace(self.main)
        for sub in ("project-api", "project-next"):
            self.init_repo(self.main / sub)
            run("git", "branch", "-M", "main", cwd=self.main / sub)
        run("git", "add", ".", cwd=self.main)
        run("git", "commit", "-qm", "tooling", cwd=self.main)
        stub = self.base / "stubs"
        stub.mkdir()
        (stub / "docker").write_text("#!/bin/sh\nexit 1\n")
        (stub / "docker").chmod(0o755)
        Runtime(Context.discover(self.main)).initialize()
        (self.main / ".git/aw-lanes.lock").touch()
        self.sleeper = subprocess.Popen(["sleep", "300"])
        self.addCleanup(close_process, self.sleeper)
        self.env = {**os.environ, "PATH": f"{stub}:{os.environ['PATH']}", "AW_PROJECT_BASE": "main",
                    "CLAUDE_CODE_SESSION_ID": "session-one", "CLAUDE_PID": str(self.sleeper.pid)}

    def agent(self, *args, env=None, ok=True, cwd=None, script=None):
        result = subprocess.run(["python3", str(script or self.main / "bin/aw"), *args], cwd=cwd or self.main,
                                env=env or self.env, text=True, capture_output=True)
        if ok and result.returncode:
            raise AssertionError(result.stderr + result.stdout)
        return result

    def test_attach_is_protected_and_idempotent(self):
        self.attach_fixture()
        (self.main / "dirty").write_text("owner work\n")
        before = run("git", "status", "--porcelain", cwd=self.main).stdout
        info = json.loads(self.agent("attach", "--json").stdout)
        self.assertEqual(info["lane"], 1)
        self.assertEqual(info["reservation"]["state"], "Attached")
        lane = self.main / ".lanes/1"
        self.assertEqual(info["paths"]["workspace"], str(lane))
        for sub in ("project-api", "project-next"):
            self.assertTrue((lane / sub / ".git").exists())
        self.assertEqual((self.main / "dirty").read_text(), "owner work\n")
        self.assertEqual(run("git", "status", "--porcelain", cwd=self.main).stdout, before)
        # The same session again: same lane, no second allocation.
        self.assertEqual(json.loads(self.agent("attach").stdout)["lane"], 1)
        self.assertEqual(len(list((self.main / ".lanes").glob("[0-9]*"))), 1)
        # From inside its lane the same session is told the same thing.
        inside = self.agent("attach", cwd=lane, script=lane / "bin/aw")
        self.assertEqual(json.loads(inside.stdout)["lane"], 1)
        # Another session gets its own lane. A lane in another lane's session is refused.
        other = {**self.env, "CLAUDE_CODE_SESSION_ID": "session-two"}
        self.assertEqual(json.loads(self.agent("attach", env=other).stdout)["lane"], 2)
        self.assertNotEqual(self.agent("attach", env={**other, "CLAUDE_CODE_SESSION_ID": "session-three"}, cwd=lane, script=lane / "bin/aw", ok=False).returncode, 0)
        # Nothing may resume or release an attached lane.
        for command, text in ((["resume", "1"], "not available"), (["delete", "1"], "attachment ownership"),
                              (["release", "1"], "invalid choice")):
            result = self.agent(*command, ok=False)
            self.assertNotEqual(result.returncode, 0, command)
            self.assertIn(text, result.stderr)
        self.assertIn("attachment ownership: Attached", self.agent("ls").stdout)
        # Identity the harness did not issue is refused; so is a harness we cannot verify.
        self.assertNotEqual(self.agent("attach", "--session-id", "forged", ok=False).returncode, 0)
        self.assertIn("Not running inside a Codex session", self.agent("attach", "--harness", "codex", ok=False).stderr)
        bare = {k: v for k, v in self.env.items() if k != "CLAUDE_CODE_SESSION_ID"}
        self.assertNotEqual(self.agent("attach", env=bare, ok=False).returncode, 0)
        # Reconciling needs an explicit --ended.
        self.assertNotEqual(self.agent("reconcile", "1", ok=False).returncode, 0)

    def test_concurrent_attach_by_one_session_makes_one_lane(self):
        self.attach_fixture()
        procs = [subprocess.Popen(["python3", str(self.main / "bin/aw"), "attach"], cwd=self.main, env=self.env,
                                  text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE) for _ in range(4)]
        results = [p.communicate() for p in procs]
        # Allocation takes the maintenance gate without waiting; a loser is told to retry.
        self.assertTrue(any(out for out, err in results), results)
        for out, err in results:
            self.assertTrue(out or "retry after they finish" in err, err)
        self.assertEqual(len(list((self.main / ".lanes").glob("[0-9]*"))), 1)
        self.assertEqual(json.loads(self.agent("attach").stdout)["lane"], 1)

    def test_attach_exhaustion_and_process_death(self):
        self.attach_fixture()
        env = {**self.env, "AW_MAX_LANES": "1"}
        self.agent("attach", env=env)
        second = {**env, "CLAUDE_CODE_SESSION_ID": "session-two"}
        self.assertIn("No lane is free", self.agent("attach", env=second, ok=False).stderr)
        # The bound process dies: the record ends and ordinary finish checks take over.
        self.sleeper.kill()
        self.sleeper.wait()
        self.assertIn("Ended", self.agent("attachments", env=env).stdout)
        self.assertNotIn("attached", self.agent("ls", env=env).stdout)

    def test_attachment_records(self):
        state = self.main / "state"
        script = str(ROOT / "tests/attachment_driver.py")

        def rec(*args, ok=True):
            return run("python3", script, *args, ok=ok)

        rec("begin", str(state), "claude", "s1", "3", "gen-a", str(os.getpid()))
        self.assertEqual(rec("protects", str(state), "3").returncode, 0)
        self.assertNotEqual(rec("protects", str(state), "4", ok=False).returncode, 0)
        # A delayed end signal from an earlier generation changes nothing.
        rec("end", str(state), "claude", "s1", "gen-old")
        self.assertEqual(rec("protects", str(state), "3").returncode, 0)
        # A reused PID (different start identity) is not the session.
        path = Path(rec("path", str(state), "claude", "s1").stdout.strip())
        data = json.loads(path.read_text())
        data["pid_start"] = "1"
        data["processes"][0]["start"] = "1"
        path.write_text(json.dumps(data))
        self.assertNotEqual(rec("protects", str(state), "3", ok=False).returncode, 0)
        self.assertEqual(json.loads(path.read_text())["state"], "Ended")
        # A new generation of the same session is protected again, and only its own end clears it.
        rec("begin", str(state), "claude", "s1", "3", "gen-b")
        rec("end", str(state), "claude", "s1", "gen-a")
        self.assertEqual(rec("protects", str(state), "3").returncode, 0)
        rec("end", str(state), "claude", "s1", "gen-b")
        self.assertNotEqual(rec("protects", str(state), "3", ok=False).returncode, 0)
        # Malformed records protect conservatively; reconcile ends real ones.
        (state / "attachments/junk.json").write_text("{not json")
        self.assertEqual(rec("protects", str(state), "9").returncode, 0)
        (state / "attachments/junk.json").unlink()
        rec("begin", str(state), "claude", "s2", "5", "gen-c")
        rec("reconcile", str(state), "5")
        self.assertNotEqual(rec("protects", str(state), "5", ok=False).returncode, 0)

    def test_resumed_process_joins_the_record(self):
        # claude --resume keeps the session id and starts a new process; the lane must stay
        # reserved until the last process that registered has exited, whichever exits first.
        self.attach_fixture()
        self.agent("attach")
        resumed = subprocess.Popen(["sleep", "300"])
        self.addCleanup(close_process, resumed)
        env = {**self.env, "CLAUDE_PID": str(resumed.pid)}
        again = self.agent("attach", env=env)
        self.assertIn("Already attached", again.stderr)
        self.assertIn("Registered process", again.stderr)
        self.assertIn(f"pid={self.sleeper.pid},{resumed.pid}", self.agent("attachments").stdout)
        # Repeating the attach adds nothing.
        self.assertNotIn("Registered process", self.agent("attach", env=env).stderr)
        # The process that attached exits first: the resumed one still holds the lane.
        self.sleeper.kill()
        self.sleeper.wait()
        self.assertIn("Attached", self.agent("attachments").stdout)
        self.assertNotIn("Ended", self.agent("attachments").stdout)
        resumed.kill()
        resumed.wait()
        self.assertIn("Ended", self.agent("attachments").stdout)

    def test_ended_session_gets_its_lane_back(self):
        # Every process exits, the conversation is resumed later: same lane, if still unused.
        self.attach_fixture()
        first = self.agent("attach")
        lane = json.loads(first.stdout)["lane"]  # stdout is only the JSON; the rest goes to stderr
        self.sleeper.kill()
        self.sleeper.wait()
        self.assertIn("Ended", self.agent("attachments").stdout)
        resumed = subprocess.Popen(["sleep", "300"])
        self.addCleanup(close_process, resumed)
        env = {**self.env, "CLAUDE_PID": str(resumed.pid)}
        again = self.agent("attach", env=env)
        self.assertIn("Returned to lane", again.stderr)
        self.assertEqual(json.loads(again.stdout)["lane"], lane)
        self.assertIn(f"pid={resumed.pid}", self.agent("attachments").stdout)
        self.assertIn("Attached", self.agent("attachments").stdout)

    def test_ended_session_does_not_take_a_lane_someone_else_holds(self):
        self.attach_fixture()
        lane = json.loads(self.agent("attach").stdout)["lane"]
        self.sleeper.kill()
        self.sleeper.wait()
        # Any later allocation of the lane writes a new generation; the old record is then stale.
        (self.main / ".lanes/.aw/lanes" / str(lane) / "generation").write_text("someone-else\n")
        resumed = subprocess.Popen(["sleep", "300"])
        self.addCleanup(close_process, resumed)
        env = {**self.env, "CLAUDE_PID": str(resumed.pid)}
        result = self.agent("attach", env=env)
        self.assertNotIn("Returned to lane", result.stderr)
        # It is allocated like any new session (possibly the same free lane, with a fresh generation).
        self.assertNotEqual(json.loads(result.stdout)["reservation"]["generation"], "someone-else")
        self.assertIn("Attached to lane", result.stderr)

    def test_read_only_git_explains_the_fix(self):
        # Codex's default sandbox keeps .git read-only; the bare shell error names nothing useful.
        self.attach_fixture()
        lock = self.main / ".git" / "aw-lanes.lock"
        lock.touch()
        lock.chmod(0o444)
        if os.access(lock, os.W_OK):
            self.skipTest("running as a user that ignores file modes")
        result = self.agent("attach", ok=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("aw-lanes.lock", result.stderr)
        self.assertNotIn("Read-only file system", result.stderr)

    def test_only_session_start_revives_an_ended_record(self):
        # Reviving on any tool call would undo the owner's reconcile.
        self.attach_fixture()
        self.agent("attach")
        self.agent("reconcile", "1", "--ended")
        state = str(self.main / ".lanes/.aw")
        script = str(ROOT / "tests/attachment_driver.py")
        payload = json.dumps({"session_id": "session-one"})
        # Not under a claude process here, so even a start finds nothing to track and revives nothing.
        for extra in ([], ["start"]):
            result = run("python3", script, "route", state, str(self.main / ".lanes"), *extra, input_text=payload, ok=False)
            self.assertNotEqual(result.returncode, 0)
        self.assertIn("Ended", self.agent("attachments").stdout)

    def test_join_requires_a_verifiable_process(self):
        state = self.main / "state"
        script = str(ROOT / "tests/attachment_driver.py")
        run("python3", script, "begin", str(state), "claude", "s1", "3", "gen-a", str(os.getpid()))
        self.assertNotEqual(run("python3", script, "join", str(state), "claude", "s1", "gen-a", "2147483646", ok=False).returncode, 0)
        self.assertNotEqual(run("python3", script, "join", str(state), "claude", "s1", "gen-old", str(os.getpid()), ok=False).returncode, 0)
        self.assertNotEqual(run("python3", script, "join", str(state), "claude", "nobody", "gen-a", str(os.getpid()), ok=False).returncode, 0)

    def test_grok_payloads_are_normalized(self):
        script = str(ROOT / "tests/payload_driver.py")

        def norm(payload):
            return json.loads(run("python3", script, input_text=json.dumps(payload)).stdout)

        grok = {"hookEventName": "pre_tool_use", "sessionId": "s", "cwd": "/x", "toolName": "run_terminal_command",
                "toolInput": {"command": "ls"}}
        self.assertEqual(norm(grok)["tool_name"], "Bash")
        self.assertEqual(norm(grok)["session_id"], "s")
        self.assertEqual(norm(grok)["tool_input"], {"command": "ls"})
        read = norm({**grok, "toolName": "read_file", "toolInput": {"target_file": "/a"}})
        self.assertEqual((read["tool_name"], read["tool_input"]), ("Read", {"file_path": "/a"}))
        self.assertEqual(norm({**grok, "toolName": "search_replace", "toolInput": {"file_path": "/a"}})["tool_name"], "Edit")
        self.assertEqual(norm({**grok, "toolName": "write", "toolInput": {"file_path": "/a"}})["tool_name"], "Write")
        self.assertEqual(norm({**grok, "toolName": "grep", "toolInput": {"path": "/a"}})["tool_name"], "Grep")
        # An unknown tool keeps its name, and its path argument is still checked.
        self.assertEqual(norm({**grok, "toolName": "mystery", "toolInput": {"target_file": "/a"}})["tool_input"], {"file_path": "/a"})
        # Claude payloads pass through unchanged.
        claude = {"session_id": "s", "tool_name": "Bash", "tool_input": {"command": "ls"}}
        self.assertEqual(norm(claude), claude)

    def test_grok_attach_and_confinement(self):
        self.attach_fixture()
        env = {k: v for k, v in self.env.items() if not k.startswith("CLAUDE")}
        env["GROK_SESSION_ID"] = "grok-session-one"
        # Without a grok process above it, the command cannot be tracked and refuses.
        refused = self.agent("attach", "--harness", "grok", env=env, ok=False)
        self.assertIn("Could not find the grok process", refused.stderr)
        # A process named grok stays alive and runs the attach, like Grok's tool shell would.
        stub = self.base / "grokbin"
        stub.mkdir()
        (stub / "grok").write_text('#!/bin/bash\npython3 "$@"\nsleep 120\n')
        (stub / "grok").chmod(0o755)
        proc = subprocess.Popen([str(stub / "grok"), str(self.main / "bin/aw"), "attach", "--harness", "grok"],
                                cwd=self.main, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.addCleanup(close_process, proc)
        for _ in range(100):
            # While the attach allocates it holds the gate exclusively, so a listing may be refused.
            if "grok" in self.agent("attachments", env=env, ok=False).stdout:
                break
            time.sleep(0.2)
        else:
            proc.kill()
            self.fail("grok attach produced no record: " + proc.communicate()[1])
        listing = self.agent("attachments", env=env).stdout
        self.assertIn(f"\tAttached\tgrok\tpid={proc.pid}\tgrok-session-one", listing)
        lane = self.main / ".lanes/1"
        hook = ["python3", str(self.main / "bin/aw"), "hook", "pre-tool-use"]

        def call(tool, tool_input, session="grok-session-one"):
            payload = json.dumps({"hookEventName": "pre_tool_use", "sessionId": session, "cwd": str(self.main),
                                  "toolName": tool, "toolInput": tool_input})
            return subprocess.run(hook, input=payload, text=True, capture_output=True).returncode

        self.assertEqual(call("run_terminal_command", {"command": "git status"}), 2)
        self.assertEqual(call("run_terminal_command", {"command": f"cd '{lane}' && git status"}), 0)
        self.assertEqual(call("run_terminal_command", {"command": f"cd '{lane}' && cat '{self.main}/AGENTS.md'"}), 2)
        self.assertEqual(call("read_file", {"target_file": str(self.main / "AGENTS.md")}), 2)
        self.assertEqual(call("read_file", {"target_file": str(lane / "AGENTS.md")}), 0)
        self.assertEqual(call("write", {"file_path": str(self.main / "x"), "content": "x"}), 2)
        self.assertEqual(call("search_replace", {"file_path": str(lane / "x"), "old_string": "a", "new_string": "b"}), 0)
        self.assertEqual(call("run_terminal_command", {"command": "git status"}, session="someone-else"), 0)
        # When the grok process is gone the mapping goes with it.
        proc.kill()
        proc.wait()
        self.assertEqual(call("run_terminal_command", {"command": "git status"}), 0)
        self.assertIn("Ended", self.agent("attachments", env=env).stdout)

    def test_codex_attach_is_adopted_by_its_hook(self):
        self.attach_fixture()
        env = {k: v for k, v in self.env.items() if not k.startswith("CLAUDE")}
        env["CODEX_SESSION_ID"] = "codex-thread-one"
        # Tool commands in Codex's sandbox cannot name the process, so attach records none;
        # the lane is reserved until a guarded tool call registers the process.
        info = json.loads(self.agent("attach", "--harness", "codex", env=env).stdout)
        self.assertEqual(info["reservation"]["state"], "Attached")
        self.assertIn("\tAttached\tcodex\tpid=-\tcodex-thread-one", self.agent("attachments", env=env).stdout)
        lane = self.main / ".lanes/1"
        payload = json.dumps({"session_id": "codex-thread-one", "cwd": str(self.main), "hook_event_name": "PreToolUse",
                              "tool_name": "Bash", "tool_input": {"command": f"cd '{lane}' && git status"}})
        stub = self.base / "codexbin"
        stub.mkdir()
        (stub / "codex").write_text(f'#!/bin/bash\ncat > "{self.base}/payload.json"\n'
                                    f'python3 "{self.main}/bin/aw" hook pre-tool-use < "{self.base}/payload.json"\n'
                                    'echo "hook rc=$?" >&2\nsleep 120\n')
        (stub / "codex").chmod(0o755)
        proc = subprocess.Popen([str(stub / "codex")], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.addCleanup(close_process, proc)
        proc.stdin.write(payload)
        proc.stdin.close()
        for _ in range(100):
            if f"pid={proc.pid}\t" in self.agent("attachments", env=env, ok=False).stdout:
                break
            time.sleep(0.2)
        else:
            proc.kill()
            self.fail("the hook did not register the codex process: " + proc.communicate()[1])
        self.assertIn(f"\tAttached\tcodex\tpid={proc.pid}\tcodex-thread-one", self.agent("attachments", env=env).stdout)
        # Once registered, the record ends with the process, as for the other harnesses.
        proc.kill()
        proc.wait()
        self.assertIn("\tEnded\tcodex\t", self.agent("attachments", env=env).stdout)

    def test_codex_patches_name_their_files(self):
        script = str(ROOT / "tests/payload_driver.py")
        patch = "*** Begin Patch\n*** Add File: a.txt\n+x\n*** Update File: sub/b.txt\n@@\n-y\n+z\n*** Move to: c.txt\n*** Delete File: d.txt\n*** End Patch"
        out = json.loads(run("python3", script, input_text=json.dumps(
            {"session_id": "s", "tool_name": "apply_patch", "tool_input": {"command": patch}})).stdout)
        self.assertEqual(out["tool_name"], "MultiEdit")
        self.assertEqual([e["file_path"] for e in out["tool_input"]["edits"]], ["a.txt", "sub/b.txt", "c.txt", "d.txt"])

    def test_mapped_session_hooks(self):
        self.attach_fixture()
        self.agent("attach")
        lane = self.main / ".lanes/1"
        owner_hook = ["python3", str(self.main / "bin/aw"), "hook", "pre-tool-use"]

        def hook(session, tool, cwd=None, **tool_input):
            payload = json.dumps({"session_id": session, "cwd": str(cwd or self.main), "tool_name": tool, "tool_input": tool_input})
            return subprocess.run(owner_hook, input=payload, text=True, capture_output=True).returncode

        def bash(command, session="session-one", cwd=None):
            return hook(session, "Bash", cwd, command=command)

        self.assertEqual(bash("bin/aw attach"), 0)
        self.assertEqual(bash("bin/aw attach && rm -rf x"), 2)
        self.assertEqual(bash("git status"), 2)  # would run against the owner's checkout
        self.assertEqual(bash("cat AGENTS.md"), 2)
        self.assertEqual(bash(f"cd '{lane}' && git status"), 0)
        self.assertEqual(bash(f"cd '{lane}' && cat ../../AGENTS.md"), 2)
        self.assertEqual(bash(f"cd '{lane}' && pkill node"), 2)
        self.assertEqual(bash(f"cd '{self.main}' && git status"), 2)
        self.assertEqual(bash("git status", cwd=lane), 0)
        self.assertEqual(hook("session-one", "Write", file_path=str(lane / "x")), 0)
        self.assertEqual(hook("session-one", "Write", file_path=str(self.main / "x")), 2)
        self.assertEqual(hook("session-one", "Write", file_path="x"), 2)
        self.assertEqual(hook("session-one", "Grep", pattern="a"), 2)
        self.assertEqual(hook("session-one", "Grep", pattern="a", path=str(lane)), 0)
        # An unattached session in the owner's checkout is left alone.
        self.assertEqual(bash("git status", session="other"), 0)
        self.assertEqual(hook("other", "Write", file_path=str(self.main / "x")), 0)
        # Once the bound process is gone the mapping goes with it.
        self.sleeper.kill()
        self.sleeper.wait()
        self.assertEqual(bash("git status"), 0)

    def test_docs_excludes_nested_worktrees(self):
        self.init_repo(self.main)
        fixtures.write_workspace(self.main)
        (self.main / "README.md").write_text("[ok](workspace.toml)\n")
        for name in (".lanes", ".worktrees"):
            nested = self.main / name / "fixture"
            nested.mkdir(parents=True)
            (nested / "STATUS.md").write_text("[broken](missing.md)")
        self.assertIn("passed", run("python3", str(self.main / "bin/aw"), "check-docs").stdout)
        (self.main / "README.md").write_text("[bad](missing.md)\n")
        self.assertIn("broken link", run("python3", str(self.main / "bin/aw"), "check-docs", ok=False).stderr)





class LifecycleTests(unittest.TestCase):
    init_repo = LaneTests.init_repo
    def setUp(self):
        LaneTests.setUp(self)
        self.init_repo(self.main)
        fixtures.write_workspace(self.main)
        run('git', 'add', '.', cwd=self.main)
        run('git', 'commit', '-qm', 'tooling', cwd=self.main)
        self.bases = {'workspace': ('', 'origin/main'), 'api': ('project-api', 'origin/main'),
                      'next': ('project-next', 'origin/main')}
        for identity, (rel, _) in self.bases.items():
            primary = self.main / rel
            if rel:
                self.init_repo(primary)
            run('git', 'branch', '-M', 'main', cwd=primary)
            remote = self.base / (identity + '-remote.git')
            run('git', 'init', '--bare', '-q', str(remote))
            run('git', 'remote', 'add', 'origin', str(remote), cwd=primary)
            run('git', 'push', '-qu', 'origin', 'main', cwd=primary)
        self.lanes = self.main / '.lanes'
        self.state = self.lanes / '.aw'
        (self.state / 'lanes').mkdir(parents=True)
        (self.state / 'slots').mkdir()
        (self.state / 'queue').touch()
        self.life = lifecycle.Lifecycle(self.main, self.lanes, self.bases)
        self.life.generated = {'project-api': ('.env', 'lib/generated/', 'node_modules/'),
                               'project-next': ('.env.development', '.env.test.local', 'node_modules/')}
        self.life.runtime = Runtime(Context.discover(self.main))
        (self.main / '.git/aw-lanes.lock').touch()

    def make_lane(self, n=1):
        path = self.lanes / str(n)
        for rel, _ in self.bases.values():
            run('git', 'worktree', 'add', '-qb', f'agent/{n}-20261022-0100', str(path / rel), 'origin/main', cwd=self.main / rel)
        (path / '.lane').write_text(f'AW_LANE={n}\n')
        s = self.state / 'lanes' / str(n)
        s.mkdir()
        (s / 'session.lock').touch()
        (s / 'activity').touch()
        return path

    def commit(self, path, text='changed\n'):
        (path / 'file').write_text(text)
        run('git', 'add', 'file', cwd=path)
        run('git', 'commit', '-qm', text.strip(), cwd=path)
        return run('git', 'rev-parse', 'HEAD', cwd=path).stdout.strip()

    def command(self, *args, lane=None, ok=True, extra_env=None):
        root = lane or self.main
        if args[:1] == ('sweep',):
            args = (*args, '--json', '--verbose')
        env = {**os.environ, 'AW_WORKSPACE_BASE': 'origin/main', 'AW_COUNT_OWNER_SERVERS': '0', **(extra_env or {})}
        result = subprocess.run(['python3', str(root / 'bin/aw'), *args], text=True, capture_output=True, env=env)
        if ok and result.returncode:
            raise AssertionError(result.stdout + result.stderr)
        if args[:1] == ('finish',) and result.returncode == 0:
            result.stdout = result.stdout.splitlines()[-1]
        return result

    def snapshot(self):
        # Hash all bytes/mode/mtime in fixture repos + state; ignore only access
        # times and the gate (whose creation is infrastructure initialization).
        import hashlib
        out = {}
        for root, dirs, files in os.walk(self.main):
            for name in files:
                p = Path(root) / name
                if p.name == 'aw-lanes.lock':
                    continue
                out[str(p.relative_to(self.main))] = (hashlib.sha256(p.read_bytes()).hexdigest(), p.stat().st_mtime_ns)
        return out

    def lane_journal(self):
        return next(data for path in (self.state / 'history').glob('*.json')
                    if (data := json.loads(path.read_text()))['entry']['kind'] == 'lane')

    def test_classifier_and_observational_cli(self):
        p = self.make_lane()
        e = self.life.lane(1)
        self.assertEqual(e['class'], 'Merged')
        self.assertEqual({r['merge_proof'] for r in e['repos']}, {'ancestry'})
        self.assertEqual(e['action'], 'keep')
        before = self.snapshot()
        for _ in range(2):
            self.command('ls')
            report = json.loads(self.command('sweep').stdout)
            self.assertEqual(report['entries'][0]['action'], 'keep warm')
        self.assertEqual(before, self.snapshot())
        (p / 'valuable').write_text('untracked')
        self.assertEqual(self.life.lane(1)['class'], 'WIP')
        self.assertIn('valuable', str(self.life.lane(1)['repos'][0]['status']))
        (p / 'valuable').unlink()
        with (self.state / 'lanes/1/session.lock').open('r') as f:
            fcntl.flock(f, fcntl.LOCK_EX)
            self.assertEqual(self.life.lane(1)['class'], 'Active / reserved')
        (self.state / 'lanes/1/parked').touch()
        self.assertEqual(self.life.lane(1)['class'], 'Parked')
        self.assertEqual(self.life.lane(1)['underlying'], 'Merged')

    def test_patch_and_squash_proofs(self):
        p = self.make_lane()
        self.commit(p)
        sha = run('git', 'rev-parse', 'HEAD', cwd=p).stdout.strip()
        # Cherry-pick onto a different parent: equivalent patch, different SHA.
        self.commit(self.main, 'base-other\n')
        run('git', 'checkout', 'HEAD~1', '--', 'file', cwd=self.main)
        run('git', 'commit', '-qm', 'restore', cwd=self.main)
        run('git', 'cherry-pick', sha, cwd=self.main)
        run('git', 'update-ref', 'refs/remotes/origin/main', 'HEAD', cwd=self.main)
        self.assertEqual(self.life.lane(1)['repos'][0]['merge_proof'], 'patch equivalence')
        self.commit(p, 'second\n')
        self.commit(p, 'third\n')
        # A multi-commit squash cannot be proven by cherry.
        self.commit(self.main, 'third\n')
        run('git', 'update-ref', 'refs/remotes/origin/main', 'HEAD', cwd=self.main)
        self.assertIsNone(self.life.lane(1)['repos'][0]['merge_proof'])

    def test_exact_mr_evidence(self):
        p = self.make_lane()
        sha = self.commit(p)
        src = {'host': 'gitlab.example', 'project': 'group/workspace', 'remote': 'origin', 'target': 'main'}
        branch = run('git', 'branch', '--show-current', cwd=p).stdout.strip()
        self.life.remote = True
        self.life.source = lambda *args: src
        detail = {'source_branch': branch, 'target_branch': 'main', 'source_project_id': 1,
                  'target_project_id': 1, 'iid': 2, 'state': 'merged', 'diff_refs': {'head_sha': sha},
                  'web_url': 'https://gitlab.example/mr/2', 'draft': False}
        api = Mock(side_effect=lambda source, endpoint: {'id': 1} if endpoint == '' else detail if endpoint == 'merge_requests/2' else [detail])
        gitlab = forge.GitLab(lambda: 5)
        gitlab.api = api
        self.life.forges[(src['host'], src['project'])] = gitlab
        self.assertEqual(self.life.lane(1)['repos'][0]['merge_proof'], 'GitLab exact source head')
        detail['diff_refs']['head_sha'] = '1' * 40
        self.assertIsNone(self.life.lane(1)['repos'][0]['merge_proof'])
        detail['diff_refs']['head_sha'] = sha
        detail['target_branch'] = 'other'
        self.assertIsNone(self.life.lane(1)['repos'][0]['merge_proof'])
        detail['target_branch'] = 'main'
        detail['source_project_id'] = 3
        self.assertIsNone(self.life.lane(1)['repos'][0]['merge_proof'])
        detail['source_project_id'] = 1
        api.side_effect = lambda *args: None
        self.assertIn('unknown', self.life.lane(1)['repos'][0]['mr']['availability'])

    def clear_forge(self):
        for adapter in self.life.forges.values():
            adapter.cache.clear()

    def test_fake_glab_structured_responses_and_dedup(self):
        from unittest.mock import patch
        directory = self.base / 'fake-glab'
        directory.mkdir()
        executable = directory / 'glab'
        executable.write_text('''#!/usr/bin/env python3
import json, os, sys
with open(os.environ['GLAB_CALLS'], 'a') as f: f.write(sys.argv[-1] + '\\n')
data = json.load(open(os.environ['GLAB_FIXTURE']))
endpoint = sys.argv[-1]
if endpoint.endswith('/merge_requests/7'): print(json.dumps(data))
elif '/merge_requests?' in endpoint: print(json.dumps([data] * int(os.environ.get('GLAB_DUPLICATES', '1'))))
else: print('{"id": 42}')
''')
        executable.chmod(0o755)
        fixture, calls = self.base / 'glab.json', self.base / 'glab.calls'
        data = {'source_branch': 'feature', 'target_branch': 'main', 'source_project_id': 42,
                'target_project_id': 42, 'iid': 7, 'state': 'opened', 'draft': True,
                'web_url': 'https://gitlab.example/g/p/-/merge_requests/7', 'diff_refs': {'head_sha': 'a' * 40}}
        fixture.write_text(json.dumps(data))
        source = {'host': 'gitlab.example', 'project': 'g/p', 'remote': 'origin', 'target': 'main'}
        self.life.remote = True
        with patch.dict(os.environ, {'PATH': str(directory) + ':' + os.environ['PATH'],
                                    'GLAB_FIXTURE': str(fixture), 'GLAB_CALLS': str(calls)}):
            evidence = self.life.mr(source, 'feature', 'a' * 40)
            self.assertTrue(evidence['draft'])
            self.assertEqual(evidence['state'], 'opened')
            self.life.mr(source, 'feature', 'a' * 40)
            self.assertEqual(len(calls.read_text().splitlines()), 3)
            with patch.dict(os.environ, {'GLAB_DUPLICATES': '2'}):
                self.clear_forge()
                self.assertEqual(self.life.mr(source, 'feature', 'a' * 40)['availability'], 'ambiguous')
            data['state'] = 'closed'
            fixture.write_text(json.dumps(data))
            self.clear_forge()
            self.assertEqual(self.life.mr(source, 'feature', 'a' * 40)['state'], 'closed')
            fixture.write_text('invalid JSON')
            self.clear_forge()
            self.assertIn('unknown', self.life.mr(source, 'feature', 'a' * 40)['availability'])

    def test_finish_open_draft_mr_and_release_failure(self):
        from unittest.mock import patch
        p = self.make_lane()
        sha = self.commit(p)
        branch = run('git', 'branch', '--show-current', cwd=p).stdout.strip()
        src = {'host': 'gitlab.example', 'project': 'g/p', 'remote': 'origin', 'target': 'main'}
        self.life.source = lambda *args: src
        self.life.mr = lambda source, b, h: {'availability': 'verified', 'state': 'opened', 'draft': True,
                  'project': src['project'], 'iid': 1, 'url': 'https://gitlab.example/mr/1', 'source_head': h}
        # Feature branches normally track origin/main; finish checks their own
        # source ref with ls-remote, not that tracking relationship.
        with self.assertRaisesRegex(ValueError, 'remote source branch'):
            self.life.finish(1, None, 'Review ready')
        run('git', 'push', '-q', 'origin', branch, cwd=p)
        with patch.object(self.life.runtime, 'stop', side_effect=ValueError('release failed')):
            with self.assertRaisesRegex(ValueError, 'Handoff saved'):
                self.life.finish(1, None, 'Review ready')
        record, error = self.life.handoff(1)
        self.assertIsNone(error)
        self.assertEqual(record['repos'][0]['mr']['source_head'], sha)
        self.assertTrue(record['repos'][0]['mr']['draft'])
        self.assertEqual(self.life.lane(1)['class'], 'Awaiting review')

    def test_finish_no_plan_idempotence_and_session(self):
        from unittest.mock import patch
        p = self.make_lane()
        with patch.object(self.life.runtime, 'stop'):
            record = self.life.finish(1, None, 'Verified work')
            self.assertEqual(self.life.finish(1, None, 'Verified work'), record)
        handoff = self.state / 'lanes/1/handoff'
        self.assertEqual(handoff.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.life.handoff(1), (record, None))
        with (self.state / 'lanes/1/session.lock').open('r') as f:
            fcntl.flock(f, fcntl.LOCK_EX)
            self.assertEqual(self.life.lane(1)['class'], 'Active / reserved')
        self.commit(p)
        self.assertIn('stale handoff', str(self.life.lane(1)['protection']))
        before = handoff.read_bytes()
        with self.assertRaisesRegex(ValueError, 'remote source branch'):
            self.life.finish(1, None, 'changed work')
        self.assertEqual(before, handoff.read_bytes())
        self.command('finish', '--no-plan', '--summary', 'x', lane=p, ok=False)
        (self.state / 'lanes/1/setup.ok').touch()
        stub = self.base / 'resume-stub'
        stub.mkdir()
        (stub / 'claude').write_text('#!/bin/sh\nexit 0\n')
        (stub / 'claude').chmod(0o755)
        self.command('resume', '1', '--harness', 'claude', ok=True,
                     extra_env={'PATH': str(stub) + ':' + os.environ['PATH']})
        self.assertFalse(handoff.exists())
        archive = next((self.state / 'history').glob('*-resume.json'))
        data = json.loads(archive.read_text())
        self.assertEqual(data['schema_version'], 1)
        self.assertEqual(data['status'], 'complete')
        data['created_at'] = 0
        lifecycle.atomic(archive, data)
        self.life.expire_journals()
        self.assertFalse(archive.exists())

    def test_local_delivery_offline_idempotent_preserves_unmerged_work(self):
        from unittest.mock import patch
        from agent_workspace.inspection import recap
        p = self.make_lane()
        sha = self.commit(p)
        run('git', 'remote', 'set-url', 'origin', 'git@gitlab.example:g/p.git', cwd=p)
        self.life.remote = True
        with patch('agent_workspace.forge.Forge.call', side_effect=AssertionError('network')), \
                patch.object(self.life, 'remote_head', side_effect=AssertionError('remote head')), \
                patch.object(self.life.runtime, 'stop') as stop:
            record = self.life.finish(1, None, 'Local result', delivery='local', reason='Owner requested local review')
            again = self.life.finish(1, None, 'Local result', delivery='local', reason='Owner requested local review')
        self.assertTrue(self.life.remote)
        self.assertEqual(record, again)
        self.assertEqual(record['schema_version'], 2)
        self.assertEqual(record['repos'][0]['head'], sha)
        self.assertEqual(self.life.handoff(1), (record, None))
        self.assertEqual(stop.call_args.args, (1,))
        self.life.remote = False
        entry = self.life.lane(1)
        self.assertNotEqual(entry['class'], 'Merged')
        self.assertFalse(entry['repos'][0]['merge_proof'])
        report = self.life.sweep(False, True, True)
        self.assertFalse(report['reuse'])
        self.assertEqual(report['entries'][0]['action'], 'keep')
        with self.assertRaisesRegex(ValueError, 'clean merged'):
            Operations = __import__('agent_workspace.operations', fromlist=['Operations']).Operations
            Operations(Context.discover(self.main)).delete(1)
        with (self.state / 'lanes/1/session.lock').open('r') as held:
            fcntl.flock(held, fcntl.LOCK_EX)
            self.assertIn('lock', str(self.life.lane(1)['protection']))
        context = Context.discover(p)
        context.settings['workspace_base'] = 'origin/main'
        effort = next(e for e in recap(context)['efforts'] if e['lanes'] == [1])
        self.assertEqual(effort['attention'], 'Your action')
        self.assertIn('delivered locally', effort['code'])
        self.assertIn('publication', effort['next_step'])
        self.assertFalse(effort['quiet'])
        self.commit(p, 'later\n')
        self.assertIn('stale handoff', str(self.life.lane(1)['protection']))
        self.assertIn('historical', str(recap(context)))

    def test_local_delivery_validation_concurrency_and_release_retry(self):
        from unittest.mock import patch
        p = self.make_lane()
        self.commit(p)
        for reason in (None, '', '   ', 'two\nlines', 'two\rlines'):
            with self.assertRaisesRegex(ValueError, 'reason'):
                self.life.finish(1, None, 'Local', delivery='local', reason=reason)
        with self.assertRaisesRegex(ValueError, 'only'):
            self.life.finish(1, None, 'Local', reason='unexpected')
        with self.assertRaisesRegex(ValueError, 'delivery'):
            self.life.finish(1, None, 'Local', delivery='anything')
        (p / 'untracked').touch()
        with self.assertRaisesRegex(ValueError, 'dirty'):
            self.life.finish(1, None, 'Local', delivery='local', reason='Review')
        (p / 'untracked').unlink()
        run('git', 'checkout', '--detach', '-q', cwd=p)
        with self.assertRaisesRegex(ValueError, 'detached'):
            self.life.finish(1, None, 'Local', delivery='local', reason='Review')
        run('git', 'checkout', '-q', 'agent/1-20261022-0100', cwd=p)
        original = lifecycle.value
        def changed(repo, *args):
            return '0' * 40 if args == ('rev-parse', 'HEAD') else original(repo, *args)
        with patch.object(lifecycle, 'value', side_effect=changed), self.assertRaisesRegex(ValueError, 'changed during'):
            self.life.finish(1, None, 'Local', delivery='local', reason='Review')
        self.assertFalse((self.state / 'lanes/1/handoff').exists())
        with patch.object(self.life.runtime, 'stop', side_effect=ValueError('release failed')), \
                self.assertRaisesRegex(ValueError, 'Handoff saved'):
            self.life.finish(1, None, 'Local', delivery='local', reason='Review')
        record, _ = self.life.handoff(1)
        with patch.object(self.life.runtime, 'stop'):
            self.assertEqual(self.life.finish(1, None, 'Local', delivery='local', reason='Review'), record)

    def test_local_handoff_reader_versions_and_resume_archive(self):
        from unittest.mock import patch
        from agent_workspace.operations import Operations
        p = self.make_lane()
        with patch.object(self.life.runtime, 'stop'):
            legacy = self.life.finish(1, None, 'Legacy')
            self.assertEqual(legacy['schema_version'], 1)
            self.commit(p)
            local = self.life.finish(1, None, 'Local', delivery='local', reason='Review')
        handoff = self.state / 'lanes/1/handoff'
        for change in ({'schema_version': 99}, {'reason': ''}, {'delivery': 'mr'}):
            handoff.write_text(json.dumps({**local, **change}))
            self.assertIn('invalid handoff', self.life.handoff(1)[1])
            with self.assertRaisesRegex(ValueError, 'inspection'):
                self.life.finish(1, None, 'Local', delivery='local', reason='Review')
        handoff.write_text(json.dumps(local))
        Operations(Context.discover(self.main)).invalidate(1)
        archived = next((self.state / 'history').glob('*-resume.json'))
        self.assertEqual(json.loads(archived.read_text())['handoff'], local)
        self.assertFalse(handoff.exists())

    def test_finish_errors_and_validation(self):
        p = self.make_lane()
        (p / 'new').touch()
        (p / 'project-api/new').touch()
        with self.assertRaises(ValueError) as err:
            self.life.finish(1, '../outside', '')
        for expected in ('summary', 'plan', 'workspace: dirty', 'api: dirty'):
            self.assertIn(expected, str(err.exception))
        for data in ({'schema_version': 2}, [], {'schema_version': 1, 'lane': 1}):
            (self.state / 'lanes/1/handoff').write_text(json.dumps(data))
            self.assertIn('invalid handoff', self.life.handoff(1)[1])
        (self.state / 'lanes/1/handoff').unlink()
        (self.state / 'lanes/1/handoff').symlink_to(self.main / 'file')
        self.assertIn('symlink', self.life.handoff(1)[1])
        with self.assertRaisesRegex(ValueError, 'escapes'):
            (p / 'plans').mkdir()
            (p / 'plans/escape.md').symlink_to(self.main / 'file')
            self.life.plan(1, 'plans/escape.md')

    def test_finish_cli_releases_only_own_slot(self):
        p = self.make_lane()
        self.make_lane(2)
        (self.state / 'slots/1').write_text('holder=lane1\ngranted=1\n')
        (self.state / 'slots/2').write_text('holder=lane2\ngranted=1\n')
        (self.state / 'queue').write_text('1 0 1 999\n1 0 2 999\n')
        result = self.command('finish', '--no-plan', '--summary', 'Verified', lane=p)
        self.assertEqual(json.loads(result.stdout)['lane'], 1)
        self.assertFalse((self.state / 'slots/1').exists())
        self.assertTrue((self.state / 'slots/2').exists())
        self.assertEqual((self.state / 'queue').read_text(), '1 0 2 999\n')
        self.assertTrue(p.exists())
        self.assertTrue((self.lanes / '2').exists())

    def test_pool_age_unknown_and_caller(self):
        for n in range(1, 5):
            self.make_lane(n)
            stamp = self.state / f'lanes/{n}/activity'
            os.utime(stamp, (100 + n, 100 + n))
        self.life.pool = 2
        self.life.days = 100000
        report = self.life.inventory(True)
        self.assertEqual([e['lane'] for e in report if e['action'] == 'keep warm'], [3, 4])
        self.life.days = 14
        self.assertTrue(all(e['action'] == 'reclaim' for e in self.life.inventory(True)))
        (self.state / 'lanes/1/activity').unlink()
        self.assertIsNone(self.life.lane(1)['age_seconds'])
        self.life.pool = 10
        self.assertEqual(self.life.inventory(True)[0]['action'], 'keep warm')
        self.life.caller = 1
        self.assertIn('caller lane', self.life.lane(1)['protection'])

    def test_plain_removal_and_ignored_artifacts(self):
        from unittest.mock import patch
        p = self.make_lane()
        (p / '.gitignore').write_text((p / '.gitignore').read_text() + '\ncache/\n__pycache__/\n')
        run('git', 'add', '.gitignore', cwd=p)
        run('git', 'commit', '-qm', 'ignore cache', cwd=p)
        run('git', 'update-ref', 'refs/remotes/origin/main', 'HEAD', cwd=p)
        (p / 'cache').mkdir()
        (p / 'cache/data').touch()
        with self.assertRaisesRegex(ValueError, 'ignored runtime'):
            self.life.apply_entry(self.life.lane(1))
        self.assertTrue(p.exists())
        shutil.rmtree(p / 'cache')
        api = p / 'project-api'
        exclude = Path(run('git', 'rev-parse', '--path-format=absolute', '--git-path', 'info/exclude', cwd=api).stdout.strip())
        exclude.write_text(exclude.read_text() + '.env\nlib/generated/\nnode_modules/\nscratch\n')
        (api / 'lib/generated/client').mkdir(parents=True)
        (api / 'lib/generated/client/index.js').touch()
        (api / 'node_modules').mkdir()
        (api / 'node_modules/x').touch()
        (api / '.env').touch()
        (api / 'scratch').touch()
        with self.assertRaisesRegex(ValueError, 'scratch'):
            self.life.apply_entry(self.life.lane(1))
        (api / 'scratch').unlink()
        (p / 'bin/lib/pkg/__pycache__').mkdir(parents=True)
        (p / 'bin/lib/pkg/__pycache__/m.cpython-314.pyc').touch()
        with patch.object(self.life, 'dispose') as dispose, patch.object(self.life, 'release_slot') as release:
            self.life.apply_entry(self.life.lane(1))
            dispose.assert_called_once_with(1)
            release.assert_called_once_with(1)
        self.assertFalse(p.exists())
        journal = self.lane_journal()
        self.assertEqual(journal['status'], 'complete')
        self.assertEqual(self.life.inventory(True), [])

    def test_change_before_apply_and_partial_failure(self):
        from unittest.mock import patch
        p = self.make_lane()
        e = self.life.lane(1)
        (p / 'precious').touch()
        with self.assertRaisesRegex(ValueError, 'changed'):
            self.life.apply_entry(e)
        (p / 'precious').unlink()
        with patch.object(self.life, 'dispose', side_effect=ValueError('storage unavailable')), patch.object(self.life, 'release_slot'):
            with self.assertRaisesRegex(ValueError, 'storage unavailable'):
                self.life.apply_entry(e)
        self.assertFalse(p.exists())
        self.assertTrue((self.state / 'lanes/1/reserved').exists())
        journal = self.lane_journal()
        self.assertEqual(journal['status'], 'failed')
        self.assertEqual(len(journal['completed']), 3)
        with patch.object(self.life, 'dispose') as dispose, patch.object(self.life, 'release_slot'):
            self.life.retry(journal['operation'])
            dispose.assert_called_once_with(1)
            self.assertEqual(self.life.retry(journal['operation'])['status'], 'complete')
        self.assertFalse((self.state / 'lanes/1/reserved').exists())

    def test_partial_worktree_retry_and_changed_head(self):
        from unittest.mock import patch
        p = self.make_lane()
        original = lifecycle.git
        fail = True
        def git_with_failure(repo, *args):
            if fail and args[:2] == ('worktree', 'remove') and str(args[2]).endswith('/project-api'):
                return subprocess.CompletedProcess(args, 1, '', 'fixture refusal')
            return original(repo, *args)
        with patch.object(lifecycle, 'git', side_effect=git_with_failure), patch.object(self.life, 'dispose'), patch.object(self.life, 'release_slot'):
            with self.assertRaisesRegex(ValueError, 'fixture refusal'):
                self.life.apply_entry(self.life.lane(1))
            self.assertFalse((p / 'project-next').exists())
            record = self.lane_journal()
            fail = False
            (p / 'precious').touch()
            with self.assertRaisesRegex(ValueError, 'changed'):
                self.life.retry(record['operation'])
            (p / 'precious').unlink()
            self.life.retry(record['operation'])
        self.assertFalse(p.exists())

    def test_stale_worktree_and_branch_refusal(self):
        from unittest.mock import patch
        p = self.main / '.worktrees/old'
        run('git', 'worktree', 'add', '-qb', 'owner-named', str(p), 'origin/main', cwd=self.main)
        entries = self.life.inventory()
        candidate = next(e for e in entries if e.get('path') == str(p))
        self.assertEqual(candidate['action'], 'remove worktree')
        self.life.apply_entry(candidate)
        self.assertFalse(p.exists())
        # Ordinary branches always use -d, and Git refusal is visible.
        branch = next(e for e in self.life.inventory() if e.get('branch') == 'owner-named' and e['kind'] == 'branch')
        original = lifecycle.git
        calls = []
        def refuse(repo, *args):
            if args[:1] == ('branch',):
                calls.append(args)
                return subprocess.CompletedProcess(args, 1, '', 'not fully merged')
            return original(repo, *args)
        with patch.object(lifecycle, 'git', side_effect=refuse):
            with self.assertRaisesRegex(ValueError, 'retained'):
                self.life.apply_entry(branch)
        self.assertEqual(calls, [('branch', '-d', 'owner-named')])
        run('git', 'worktree', 'add', '-qb', 'unique', str(p), 'origin/main', cwd=self.main)
        self.commit(p)
        self.assertEqual(next(e for e in self.life.inventory() if e.get('path') == str(p))['action'], 'report only')

    def fake_docker(self):
        directory = self.base / 'fake-bin'
        directory.mkdir(exist_ok=True)
        executable = directory / 'docker'
        executable.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$DOCKER_CALLS"\ncase "$*" in\n *head-bucket*) echo "404 Not Found" >&2; exit 1;;\n inspect*) exit 1;;\n ps*) printf "agent-api\\nagent-next\\n";;\nesac\n')
        executable.chmod(0o755)
        return {'PATH': str(directory) + ':' + os.environ['PATH'], 'DOCKER_CALLS': str(self.base / 'docker-calls')}

    def test_cli_apply_callbacks_and_idempotence(self):
        self.make_lane()
        env = self.fake_docker()
        report = json.loads(self.command('sweep', '--apply', extra_env={**env, 'AW_LANE_POOL': '0'}).stdout)
        self.assertFalse(report['errors'])
        self.assertFalse((self.lanes / '1').exists())
        calls = (self.base / 'docker-calls').read_text()
        self.assertIn('DROP DATABASE IF EXISTS "aw-lane1"', calls)
        self.assertIn('DROP DATABASE IF EXISTS "aw-lane1-test"', calls)
        self.command('sweep', '--apply', extra_env={**env, 'AW_LANE_POOL': '0'})
        self.assertEqual(calls, (self.base / 'docker-calls').read_text())

    def test_cli_new_reserves_reuse_with_pool_zero(self):
        p = self.make_lane()
        env = self.fake_docker()
        # Setup remains asynchronous; a small harness exercises the actual new
        # dispatcher and checks the protected lane from a concurrent command.
        harness = self.base / 'claude'
        harness.write_text('#!/bin/sh\ntouch "$HARNESS_READY"\nwhile [ ! -e "$HARNESS_DONE" ]; do sleep 0.1; done\n')
        harness.chmod(0o755)
        ready, done = self.base / 'ready', self.base / 'done'
        process = subprocess.Popen(['python3', str(self.main / 'bin/aw'), 'new', '--harness', 'claude'],
            env={**os.environ, **env, 'AW_LANE_POOL': '0', 'AW_WORKSPACE_BASE': 'origin/main',
                 'HARNESS_READY': str(ready), 'HARNESS_DONE': str(done), 'PATH': str(self.base) + ':' + os.environ['PATH']}, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.addCleanup(close_process, process)
        import time
        deadline = time.monotonic() + 8
        while not ready.exists() and process.poll() is None and time.monotonic() < deadline:
            time.sleep(0.05)
        if not ready.exists():
            process.kill()
            out, err = process.communicate()
            self.fail(out + err)
        try:
            self.assertTrue(p.exists())
            self.assertTrue(lifecycle.locked_file(self.state / 'lanes/1/session.lock'))
            self.assertEqual(self.life.lane(1)['class'], 'Active / reserved')
            self.assertFalse((self.state / 'lanes/1/claimed').exists())
        finally:
            done.touch()
            out, err = process.communicate(timeout=10)
        self.assertEqual(process.returncode, 0, out + err)

    def test_backup_locked_symlink_detached_and_unknown_session(self):
        p = self.make_lane()
        (p / '.lane').write_text('AW_LANE=2\n')
        self.assertIn('invalid lane marker', self.life.lane(1)['protection'])
        (p / '.lane').write_text('AW_LANE=1\n')
        (self.main / '.worktrees/backup').mkdir(parents=True)
        report = self.life.inventory()
        self.assertEqual(next(e for e in report if e.get('path', '').endswith('/backup'))['action'], 'report only')
        run('git', 'worktree', 'lock', str(p), cwd=self.main)
        self.assertTrue(self.life.lane(1)['protection'])
        run('git', 'worktree', 'unlock', str(p), cwd=self.main)
        run('git', 'checkout', '--detach', cwd=p)
        self.assertEqual(self.life.lane(1)['underlying'], 'Unknown')
        (self.state / 'lanes/1/session.lock').unlink()
        self.assertIn('ownership unknown', str(self.life.lane(1)['protection']))
        (self.lanes / '2').symlink_to(self.main)
        self.assertIn('symlink', str(self.life.lane(2)))

    def test_gate_and_held_session_allocation(self):
        p = self.make_lane()
        (self.main / '.git/aw-lanes.lock').touch()
        with (self.main / '.git/aw-lanes.lock').open('r') as f:
            fcntl.flock(f, fcntl.LOCK_SH)
            result = self.command('sweep', '--apply', ok=False)
            self.assertIn('active', result.stderr)
        with (self.state / 'lanes/1/session.lock').open('r') as f:
            fcntl.flock(f, fcntl.LOCK_EX)
            self.assertEqual(self.life.inventory(True)[0]['class'], 'Active / reserved')
            self.assertNotEqual(self.command('release', '1', '--force', ok=False).returncode, 0)
            self.assertNotEqual(self.command('resume', '1', '--', 'true', ok=False).returncode, 0)
        for marker in ('claimed', 'reserved'):
            (self.state / 'lanes/1' / marker).touch()
            self.assertTrue(self.life.lane(1)['protection'])
            (self.state / 'lanes/1' / marker).unlink()

    def test_attachment_reservations_are_observational(self):
        self.make_lane()
        attachments = self.state / 'attachments'
        attachments.mkdir()
        record = attachments / 'claude-session.json'
        for state in ('Attaching', 'Attached', 'Uncertain'):
            record.write_text(json.dumps({'schema_version': 1, 'lane': 1, 'state': state}))
            before = record.read_bytes(), record.stat().st_mtime_ns
            self.assertIn('attachment ownership', str(self.life.lane(1)['protection']))
            self.assertNotEqual(self.command('resume', '1', '--', 'true', ok=False).returncode, 0)
            self.assertNotEqual(self.command('release', '1', '--force', ok=False).returncode, 0)
            self.assertEqual(before, (record.read_bytes(), record.stat().st_mtime_ns))
        record.write_text(json.dumps({'schema_version': 1, 'lane': 1, 'state': 'Ended'}))
        self.assertEqual(self.life.lane(1)['class'], 'Merged')
        record.write_text('{bad')
        self.assertIn('uncertain attachment', str(self.life.lane(1)['protection']))

    def test_push_states_and_remote_mapping(self):
        p = self.make_lane()
        # Local fixture paths are deliberately not GitLab identities.
        self.assertIsNone(self.life.source(p, 'origin/main'))
        src = {'host': 'gitlab.example', 'project': 'g/p', 'remote': 'origin', 'target': 'main'}
        self.life.source = lambda *args: src
        self.assertIn('source ref missing', self.life.lane(1)['repos'][0]['push'])
        run('git', 'push', '-q', 'origin', 'HEAD', cwd=p)
        self.assertEqual(self.life.lane(1)['repos'][0]['push'], 'covered by local remote ref')
        self.commit(p)
        self.assertEqual(self.life.lane(1)['repos'][0]['push'], 'unpushed')
        self.assertEqual(self.life.lane(1)['class'], 'WIP')
        run('git', 'update-ref', '-d', 'refs/remotes/origin/agent/1-20261022-0100', cwd=p)
        self.assertNotEqual(self.life.lane(1)['repos'][0]['push'], 'unpushed')

if __name__ == "__main__":
    unittest.main(verbosity=2)
