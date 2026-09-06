"""Issue #36: the pinned codegraph initialized once in every checkout sum creates, one local index per worktree, explicit degradation.

Runs against the strict fake codegraph (`tests/fixtures/codegraph.py`, scripted from the real 1.5.0 behavior observed in a lab), the fake
Herdr, real Git, and a designated lab store. `RealCodegraphTest` repeats the isolation cases against a real binary when
SUM_REAL_CODEGRAPH_BIN names one; it is skipped otherwise so the suite stays offline."""
from __future__ import annotations
import argparse
import concurrent.futures
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("test_core", ROOT / "tests/test_core.py")
core = importlib.util.module_from_spec(spec)
spec.loader.exec_module(core)
sumctl = core.sumctl
FAKE = ROOT / "tests/fixtures/codegraph.py"


class GraphLab(core.CoreTest):
    """The core lab plus a fake codegraph and an empty HOME, so any global configuration write would be visible."""
    def setUp(self):
        super().setUp()
        self.cg_root = self.root / "fake-codegraph"
        self.home_dir = self.root / "home"
        self.home_dir.mkdir()
        patch = mock.patch.dict(os.environ, {"SUM_CODEGRAPH_BIN": str(FAKE), "FAKE_CODEGRAPH_ROOT": str(self.cg_root), "HOME": str(self.home_dir),
                                             "SUM_GRAPH_TIMEOUT": "", "FAKE_CODEGRAPH_FAIL": "", "FAKE_CODEGRAPH_SLEEP": "", "FAKE_CODEGRAPH_VERSION": ""})
        patch.start()
        self.addCleanup(patch.stop)
        for key in ("SUM_GRAPH_TIMEOUT", "FAKE_CODEGRAPH_FAIL", "FAKE_CODEGRAPH_SLEEP", "FAKE_CODEGRAPH_VERSION"):
            os.environ.pop(key, None)

    def cg_calls(self):
        path = self.cg_root / "calls.jsonl"
        return [json.loads(line)["args"] for line in path.read_text().splitlines()] if path.exists() else []

    def graph(self, task):
        return sumctl.read_graph(self.store, task["id"])

    def graph_init(self, task):
        return sumctl.graph_init_task(self.store, argparse.Namespace(task=task["id"]))

    def graph_status(self, task):
        return sumctl.graph_status_task(self.store, argparse.Namespace(task=task["id"]))

    def status_json(self, path):
        result = subprocess.run([sys.executable, str(FAKE), "status", "--json", str(path)], capture_output=True, text=True, check=True)
        return json.loads(result.stdout)

    def query(self, path, name):
        result = subprocess.run([sys.executable, str(FAKE), "query", name, "-p", str(path), "--json"], capture_output=True, text=True, check=True)
        return json.loads(result.stdout)

    def commit_py(self, cwd, relative, text, message="change"):
        target = Path(cwd) / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)
        self.git("add", relative, cwd=cwd)
        self.git("commit", "-q", "-m", message, cwd=cwd)
        return self.git("rev-parse", "HEAD", cwd=cwd)

    def shell(self, command, cwd=None):
        return subprocess.run(["sh", "-c", command], cwd=cwd, capture_output=True, text=True)

    def brief_section(self, task):
        text = Path(task["brief_path"]).read_text()
        start = text.index("## Code graph")
        return text[start:text.index("## Delivered runtime")]


class GraphTest(GraphLab):
    def test_prepare_initializes_a_local_index_in_the_task_checkout_only(self):
        self.commit_py(self.repo, "src/a.py", "def alpha():\n    return 1\n")
        task = self.prepare()
        worktree = Path(task["worktree"])
        record = self.graph(task)
        self.assertEqual((task["status"], record["state"], record["purpose"]), ("prepared", "ready", "task"))
        self.assertEqual(record["index_path"], str(worktree / ".codegraph"))
        self.assertTrue((worktree / ".codegraph" / "codegraph.db").is_file())
        self.assertFalse((self.repo / ".codegraph").exists(), "the primary clone never gets the task's index")
        self.assertEqual(record["identity"], {"worktree": str(worktree), "head": task["base_sha"], "branch": task["branch"],
                                              "git_common_dir": str((self.repo / ".git").resolve())})
        self.assertEqual((record["tool"]["version"], record["tool"]["pinned"], record["tool"]["available"]), ("1.5.0", sumctl.CODEGRAPH_VERSION, True))
        self.assertEqual([a["action"] for a in record["attempts"]], ["init"])
        self.assertTrue(record["attempts"][0]["ok"] and record["attempts"][0]["seconds"] >= 0 and record["attempts"][0]["exit"] == 0)
        self.assertEqual((record["index"]["fileCount"], record["index"]["built_with"], record["freshness"]["state"]), (2, "1.5.0", "fresh"))
        self.assertEqual(task["graph"]["state"], "ready")
        self.assertEqual(self.store.read(task["id"])["graph"]["files"], 2)
        self.assertEqual(record["exclude"]["state"], "written")
        exclude = Path(record["exclude"]["path"])
        self.assertEqual(exclude, (self.repo / ".git" / "info" / "exclude").resolve())
        self.assertIn(".codegraph/\n", exclude.read_text())
        self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all", "--ignored=matching", cwd=worktree), "!! .codegraph/")
        self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all", cwd=self.repo), "")
        for call in self.cg_calls():
            self.assertNotIn(call[0], {"install", "serve", "upgrade", "uninstall", "uninit"})
        env = json.loads((self.cg_root / "calls.jsonl").read_text().splitlines()[-1])["env"]
        self.assertEqual((env["CODEGRAPH_NO_DAEMON"], env["CODEGRAPH_NO_DOWNLOAD"]), ("1", "1"))
        section = self.brief_section(task)
        self.assertIn("State: `ready`", section)
        self.assertIn(record["commands"]["sync"], section)
        self.assertIn(record["commands"]["explore"], section)
        self.assertIn("CODEGRAPH_NO_DAEMON=1", record["commands"]["status"])
        self.assertIn("never perpetually current", section)
        self.assertIn("replace no verification command", section)
        self.assertIn("Do not run `codegraph install`", section)
        view = sumctl.context_view(self.store, task["id"], argparse.Namespace(role="worker", section=None, since=None, limit=20, after=0, max_chars=4000, kind=None))
        self.assertEqual((view["execution"]["graph"]["state"], view["execution"]["graph"]["commands"]["sync"], view["outline"]["graph"]),
                         ("ready", record["commands"]["sync"], "ready"))
        self.assertEqual(sumctl.status(self.store)["tasks"][0]["graph"], "ready")

    def test_two_worktrees_of_one_repository_return_their_own_graphs(self):
        self.commit_py(self.repo, "src/a.py", "def alpha():\n    return 0\n")
        sumctl.write_settings(self.store, capacity={"per_repository": 2})
        first, second = self.prepare(), self.prepare()
        self.commit_py(first["worktree"], "src/a.py", "def alpha():\n    return 'first'\n")
        self.commit_py(second["worktree"], "src/a.py", "def alpha():\n    return 'second'\n\ndef beta():\n    return alpha()\n")
        for task in (first, second):
            self.assertEqual(self.graph_status(task)["live"]["freshness"]["state"], "stale")
            record = self.graph_init(task)["graph"]
            self.assertEqual((record["state"], record["attempts"][-1]["action"]), ("ready", "sync"))
        self.assertEqual([r["node"]["source"] for r in self.query(first["worktree"], "alpha")], ["def alpha():"])
        self.assertEqual(self.status_json(first["worktree"])["nodeCount"], 3)
        self.assertEqual(self.status_json(second["worktree"])["nodeCount"], 4)
        self.assertEqual([r["node"]["name"] for r in self.query(second["worktree"], "beta")], ["beta"])
        self.assertEqual(self.query(first["worktree"], "beta"), [])
        self.assertEqual(self.status_json(self.repo)["initialized"], False)
        self.assertNotEqual(self.status_json(first["worktree"])["indexPath"], self.status_json(second["worktree"])["indexPath"])
        self.assertIsNone(self.status_json(second["worktree"])["worktreeMismatch"])

    def test_repeat_init_reconciles_without_a_rebuild_and_stale_edits_are_reported_honestly(self):
        self.commit_py(self.repo, "src/a.py", "def alpha():\n    return 1\n")
        task = self.prepare()
        before = json.loads((Path(task["worktree"]) / ".codegraph" / "meta.json").read_text())
        calls_before = len(self.cg_calls())
        again = self.graph_init(task)["graph"]
        self.assertEqual((again["state"], again["attempts"][-1]["action"], again["attempts"][-1]["ok"]), ("ready", "verified", True))
        self.assertEqual(json.loads((Path(task["worktree"]) / ".codegraph" / "meta.json").read_text()), before)
        self.assertEqual([c[0] for c in self.cg_calls()[calls_before:]], ["--version", "status", "status"])
        (Path(task["worktree"]) / "src" / "a.py").write_text("def alpha():\n    return 2\n\ndef gamma():\n    return 3\n")
        status = self.graph_status(task)
        self.assertEqual((status["live"]["freshness"]["state"], status["live"]["freshness"]["pending"]["modified"], status["live"]["reconcile_needed"]), ("stale", 1, "sync"))
        self.assertEqual(status["live"]["freshness"]["dirty_files"], 1)
        self.assertEqual(self.query(task["worktree"], "gamma"), [])
        synced = self.shell(again["commands"]["sync"])
        self.assertEqual(synced.returncode, 0, synced.stderr)
        self.assertEqual(self.graph_status(task)["live"]["freshness"]["state"], "fresh")
        self.assertEqual([r["node"]["startLine"] for r in self.query(task["worktree"], "gamma")], [4])
        self.assertEqual(self.graph_status(task)["recorded"]["state"], "ready")

    def test_ignored_trees_stay_out_of_the_index_and_an_already_ignoring_repository_gets_no_exclude_write(self):
        (self.repo / ".gitignore").write_text("projects/\n.sum/\n.local/\n")
        self.commit_py(self.repo, "src/a.py", "def alpha():\n    return 1\n")
        self.git("add", ".gitignore")
        self.git("commit", "-q", "-m", "ignore")
        task = self.prepare()
        worktree = Path(task["worktree"])
        for relative, text in (("projects/o/r/n.py", "def nested_secret():\n    return 9\n"), (".sum/state.py", "TOKEN = 'x'\n"), (".local/bin/tool.py", "def tool():\n    pass\n")):
            (worktree / relative).parent.mkdir(parents=True)
            (worktree / relative).write_text(text)
        record = self.graph_init(task)["graph"]
        self.assertEqual(record["state"], "ready")
        db = json.loads((worktree / ".codegraph" / "codegraph.db").read_text())
        self.assertEqual(sorted(db["files"]), [".gitignore", "README.md", "src/a.py"])
        self.assertEqual(self.query(worktree, "nested_secret"), [])
        (self.repo / ".gitignore").write_text("projects/\n.codegraph/\n")
        self.git("add", ".gitignore")
        self.git("commit", "-q", "-m", "ignore index")
        second_repo = self.root / "second"
        shutil.copytree(self.repo, second_repo)
        exclude = second_repo / ".git" / "info" / "exclude"
        untouched = exclude.read_text() if exclude.is_file() else None
        task = self.prepare(repo=str(second_repo))
        record = self.graph(task)
        self.assertEqual((record["state"], record["exclude"]), ("ready", {"state": "already-ignored", "path": None}))
        self.assertEqual(exclude.read_text() if exclude.is_file() else None, untouched)

    def test_failed_init_keeps_task_and_checkout_and_retries_stay_bounded(self):
        with mock.patch.dict(os.environ, {"FAKE_CODEGRAPH_FAIL": "1"}):
            task = self.prepare()
            record = self.graph(task)
            self.assertEqual((task["status"], record["state"]), ("prepared", "failed"))
            self.assertIn("indexing failed as instructed", record["error"])
            self.assertTrue(Path(task["worktree"]).is_dir())
            self.assertFalse((Path(task["worktree"]) / ".codegraph" / "meta.json").exists())
            section = self.brief_section(task)
            self.assertIn("State: `failed`", section)
            self.assertIn(sumctl.GRAPH_FALLBACK, section)
            self.assertIn(f"graph init {task['id']}", section)
            self.assertIn("Do not run `codegraph init`", section)
            started = sumctl.start(self.store, task["id"])
            self.assertEqual(started["status"], "running")
            with self.assertRaisesRegex(sumctl.SumError, "Only a prepared task"):
                sumctl.start(self.store, task["id"])
            second = self.graph_init(task)["graph"]
            self.assertEqual((second["state"], len(second["attempts"])), ("failed", 2))
        brief_before = Path(task["brief_path"]).read_text()
        recovered = self.graph_init(task)["graph"]
        self.assertEqual((recovered["state"], [a["action"] for a in recovered["attempts"]]), ("ready", ["init", "init", "init"]))
        self.assertEqual(len(sumctl.graph_failures(recovered)), 2)
        saved = self.store.read(task["id"])
        self.assertEqual((saved["status"], saved["graph"]["state"], saved["graph"]["failures"]), ("running", "ready", 2))
        self.assertEqual(Path(task["brief_path"]).read_text(), brief_before)
        view = sumctl.context_view(self.store, task["id"], argparse.Namespace(role=None, section=["execution"], since=None, limit=20, after=0, max_chars=4000, kind=None))
        self.assertEqual(view["execution"]["graph"]["state"], "ready")
        self.assertEqual(sum(1 for c in self.calls() if c[:2] == ["agent", "start"]), 1)

    def test_three_failures_exhaust_and_the_fallback_is_the_record(self):
        with mock.patch.dict(os.environ, {"FAKE_CODEGRAPH_FAIL": "1"}):
            task = self.prepare()
            self.assertEqual(self.graph_init(task)["graph"]["state"], "failed")
            third = self.graph_init(task)["graph"]
            self.assertEqual((third["state"], len(sumctl.graph_failures(third))), ("failed", 3))
        exhausted = self.graph_init(task)["graph"]
        self.assertEqual(exhausted["state"], "exhausted")
        self.assertIn("source inspection", exhausted["error"])
        with self.assertRaisesRegex(sumctl.SumError, "exhausted after 3 failed attempts"):
            self.graph_init(task)
        self.assertEqual(len(self.graph(task)["attempts"]), 3)
        self.assertFalse((Path(task["worktree"]) / ".codegraph" / "meta.json").exists())
        self.assertEqual(self.store.read(task["id"])["status"], "prepared")

    def test_timeout_stops_the_build_and_records_it_without_touching_the_task(self):
        with mock.patch.dict(os.environ, {"FAKE_CODEGRAPH_SLEEP": "5", "SUM_GRAPH_TIMEOUT": "1"}):
            task = self.prepare()
        record = self.graph(task)
        attempt = record["attempts"][-1]
        self.assertEqual((record["state"], attempt["action"], attempt["timed_out"], attempt["ok"], attempt["exit"]), ("failed", "init", True, False, None))
        self.assertLess(attempt["seconds"], 4)
        self.assertIn("did not finish within 1s", record["error"])
        for marker in (self.cg_root / "active").iterdir():
            with self.assertRaises(ProcessLookupError):
                os.kill(int(marker.name), 0)
        self.assertEqual(self.store.read(task["id"])["status"], "prepared")
        recovered = self.graph_init(task)["graph"]
        self.assertEqual((recovered["state"], recovered["attempts"][-1]["action"]), ("ready", "init"))

    def test_slots_bound_concurrent_builds_and_a_full_house_is_deferred_not_queued(self):
        sumctl.write_settings(self.store, capacity={"global": 3})
        tasks = [self.prepare(repo=str(self.repo)) for _ in range(1)]
        for name in ("two", "three"):
            repo = self.root / name
            shutil.copytree(self.repo, repo)
            tasks.append(self.prepare(repo=str(repo)))
        for task in tasks:
            shutil.rmtree(Path(task["worktree"]) / ".codegraph")
        (self.cg_root / "peak.txt").unlink(missing_ok=True)
        with mock.patch.dict(os.environ, {"FAKE_CODEGRAPH_SLEEP": "0.6", "SUM_GRAPH_TIMEOUT": "10"}):
            with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
                results = list(pool.map(lambda t: self.graph_init(t)["graph"]["state"], tasks))
        self.assertEqual(results, ["ready"] * 3)
        self.assertLessEqual(int((self.cg_root / "peak.txt").read_text()), sumctl.GRAPH_SLOTS)
        shutil.rmtree(Path(tasks[0]["worktree"]) / ".codegraph")
        holders = [(sumctl.graph_slot_dir(self.store) / f"{i}.lock").open("a") for i in range(sumctl.GRAPH_SLOTS)]
        for handle in holders:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            with mock.patch.dict(os.environ, {"SUM_GRAPH_TIMEOUT": "1"}):
                deferred = self.graph_init(tasks[0])["graph"]
        finally:
            for handle in holders:
                fcntl.flock(handle, fcntl.LOCK_UN)
                handle.close()
        self.assertEqual((deferred["state"], deferred["attempts"][-1]["action"]), ("deferred", "deferred"))
        self.assertIn("no index slot free within 1s", deferred["error"])
        self.assertEqual(len(sumctl.graph_failures(deferred)), 0, "a deferral is not a failed attempt")
        self.assertFalse((Path(tasks[0]["worktree"]) / ".codegraph").exists())
        self.assertEqual(self.store.read(tasks[0]["id"])["status"], "prepared")
        self.assertEqual(self.graph_init(tasks[0])["graph"]["state"], "ready")

    def test_missing_or_mismatched_tool_degrades_explicitly_and_changes_nothing_else(self):
        with mock.patch.dict(os.environ, {"SUM_CODEGRAPH_BIN": ""}), mock.patch.object(sumctl, "GRAPH_BIN", self.root / "runtime-without-codegraph" / ".local" / "bin" / "codegraph"):
            os.environ.pop("SUM_CODEGRAPH_BIN")
            task = self.prepare()
            record = self.graph(task)
            self.assertEqual((task["status"], record["state"], record["tool"]["available"]), ("prepared", "unavailable", False))
            self.assertIn("not installed in this runtime", record["error"])
            self.assertEqual(record["tool"]["path"], str(self.root / "runtime-without-codegraph" / ".local" / "bin" / "codegraph"))
            self.assertFalse((Path(task["worktree"]) / ".codegraph").exists())
            self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all", cwd=task["worktree"]), "")
            self.assertIn("State: `unavailable`", self.brief_section(task))
            self.assertEqual(self.graph_init(task)["graph"]["state"], "unavailable")
            with self.assertRaisesRegex(sumctl.SumError, "No snippet"):
                sumctl.graph_config(self.store, argparse.Namespace(harness="claude", raw=False))
        with mock.patch.dict(os.environ, {"FAKE_CODEGRAPH_VERSION": "1.6.0"}):
            record = self.graph_init(task)["graph"]
            self.assertEqual((record["state"], record["tool"]["version"]), ("unavailable", "1.6.0"))
            self.assertIn("not the tested pin 1.5.0", record["error"])
            self.assertEqual([c for c in self.cg_calls() if c[0] in ("init", "index", "sync")], [])
        doctor = sumctl.doctor(self.store)
        row = next(c for c in doctor["checks"] if c["tool"] == "codegraph")
        self.assertEqual((row["ok"], row["available"], row["version"], row["pinned"]), (True, True, "1.5.0", "1.5.0"))
        self.assertEqual(self.graph_init(task)["graph"]["state"], "ready")

    def test_no_global_configuration_or_permission_is_written_and_config_is_print_only(self):
        task = self.prepare()
        before = {str(p.relative_to(self.root)): p.stat().st_mtime_ns for p in self.root.rglob("*") if p.is_file()}
        value = sumctl.graph_config(self.store, argparse.Namespace(harness="claude", raw=False))
        snippet = json.loads(value["snippet"])
        self.assertEqual(snippet["mcpServers"]["codegraph"], {"type": "stdio", "command": str(FAKE), "args": ["serve", "--mcp"]})
        self.assertNotIn("permissions", value["snippet"])
        self.assertIn("wrote no file", value["note"])
        codex = sumctl.graph_config(self.store, argparse.Namespace(harness="codex", raw=False))
        self.assertIn('[mcp_servers.codegraph]', codex["snippet"])
        self.assertIn(json.dumps(str(FAKE)), codex["snippet"])
        cursor = sumctl.graph_config(self.store, argparse.Namespace(harness="cursor", raw=False))
        self.assertIn("${workspaceFolder}", cursor["snippet"])
        opencode = json.loads(sumctl.graph_config(self.store, argparse.Namespace(harness="opencode", raw=False))["snippet"])
        self.assertEqual(opencode["mcp"]["codegraph"]["command"][0], str(FAKE))
        with self.assertRaisesRegex(sumctl.SumError, "--harness must be one of"):
            sumctl.graph_config(self.store, argparse.Namespace(harness="grok", raw=False))
        after = {str(p.relative_to(self.root)): p.stat().st_mtime_ns for p in self.root.rglob("*") if p.is_file()}
        changed = {k for k in set(before) | set(after) if before.get(k) != after.get(k)}
        self.assertEqual({k for k in changed if not k.startswith("fake-codegraph/")}, set(), changed)
        self.assertEqual(sorted(p.name for p in self.home_dir.iterdir()), [])
        self.assertEqual([c for c in self.cg_calls() if c[0] in ("install", "serve", "upgrade", "uninstall")], [])
        raw = self.cli("graph", "config", "--harness", "claude", "--raw")
        self.assertEqual(json.loads(raw.stdout)["mcpServers"]["codegraph"]["command"], str(FAKE))
        self.assertEqual(sorted(p.name for p in self.home_dir.iterdir()), [])

    def test_cleanup_classifies_the_index_as_disposable_and_backups_carry_rebuild_metadata_only(self):
        task = self.prepare()
        worktree = Path(task["worktree"])
        (worktree / "notes.log").write_text("keep\n")
        artifacts = sumctl.worktree_artifacts(task["worktree"])
        self.assertEqual((artifacts["ignored_disposable"], artifacts["untracked"]), ([".codegraph/"], ["notes.log"]))
        self.assertTrue(sumctl.disposable_ignored(".codegraph/codegraph.db"))
        destination = self.root / "backup.tar.gz"
        result = sumctl.backup(self.store, destination)
        graph = result["manifest"]["graph"]
        self.assertEqual((graph["indexes_included"], graph["rebuild"][0]["task"], graph["rebuild"][0]["state"], graph["rebuild"][0]["tool_version"]),
                         (False, task["id"], "ready", "1.5.0"))
        self.assertIn(str(worktree), graph["rebuild"][0]["rebuild"])
        with tarfile.open(destination) as archive:
            names = archive.getnames()
        self.assertIn(f"state/tasks/{task['id']}/graph.json", names)
        self.assertFalse(any("codegraph.db" in n or ".codegraph" in n for n in names))

    def test_dev_prepare_initializes_the_development_checkout_and_reopening_reconciles(self):
        root, store = self.installation()
        (root / "lib.py").write_text("def helper():\n    return 1\n")
        self.git("add", "lib.py", cwd=root)
        self.git("commit", "-q", "-m", "lib", cwd=root)
        prepared = self.dev(store, "graph-topic")
        path = Path(prepared["path"])
        self.assertEqual((prepared["graph"]["state"], prepared["graph"]["purpose"], prepared["graph"]["index_path"]), ("ready", "development", str(path / ".codegraph")))
        self.assertEqual(prepared["graph"]["attempts"][-1]["action"], "init")
        self.assertEqual(json.loads((path / ".sum" / "dev.json").read_text())["graph"]["state"], "ready")
        self.assertFalse((root / ".codegraph").exists())
        self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all", cwd=path), "")
        self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all", cwd=root), "")
        reopened = self.dev(store, "graph-topic")
        self.assertEqual((reopened["reopened"], reopened["graph"]["state"], reopened["graph"]["attempts"][-1]["action"]), (True, "ready", "verified"))
        (path / "lib.py").write_text("def helper():\n    return 2\n")
        again = self.dev(store, "graph-topic")
        self.assertEqual((again["graph"]["attempts"][-1]["action"], again["graph"]["freshness"]["state"]), ("sync", "fresh"))
        self.assertEqual([r["node"]["name"] for r in self.query(path, "helper")], ["helper"])


class GraphRootVerificationTest(GraphLab):
    """The root verification checkout (`verify --execute`) gets its own index, and it leaves with that checkout."""
    def setUp(self):
        super().setUp()
        for item in (ROOT / "tests/fixtures/verify/cli").iterdir():
            (shutil.copytree if item.is_dir() else shutil.copy2)(item, self.repo / item.name)
        shutil.copytree(ROOT / ".agents/skills/verify", self.repo / ".agents/skills/verify")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "standardized fixture")
        self.base = self.git("rev-parse", "HEAD")
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "mise").symlink_to(ROOT / "tests/fixtures/mise.py")
        patch = mock.patch.dict(os.environ, {"PATH": os.pathsep.join([str(self.bin), str(Path(sys.executable).resolve().parent), "/usr/bin", "/bin"]),
                                             "SUM_GH_BIN": str(ROOT / "tests/fixtures/gh.py"), "FAKE_GH_ROOT": str(self.root / "fake-gh")})
        patch.start()
        self.addCleanup(patch.stop)

    def test_root_verification_checkout_gets_a_separate_index_that_leaves_with_it(self):
        task = self.prepare()
        worker_index = json.loads((Path(task["worktree"]) / ".codegraph" / "meta.json").read_text())
        sha = self.commit_py(task["worktree"], "NOTES.md", "notes\n")
        result = sumctl.verify(self.store, argparse.Namespace(task=task["id"], candidate=sha, result=None, text=None, file=None, run=None, execute=True, base=None))
        record = result["evidence"]
        self.assertEqual((record["result"], record["isolation"]), ("pass", "separate-checkout"))
        self.assertEqual((record["graph"]["state"], record["graph"]["last_action"]), ("ready", "init"))
        self.assertTrue(record["graph"]["index_path"].startswith(str(self.store.path(task["id"]) / "verification")))
        self.assertFalse(Path(record["root"]).exists(), "the verification checkout and its index are removed together")
        self.assertEqual(json.loads((Path(task["worktree"]) / ".codegraph" / "meta.json").read_text()), worker_index, "the worker's index is never touched")
        self.assertEqual(record["certifies"], sha)
        inits = [c for c in self.cg_calls() if c[0] == "init"]
        self.assertEqual(len(inits), 2)
        self.assertNotEqual(Path(inits[0][1]).resolve(), Path(inits[1][1]).resolve())


class GraphReleaseTest(core.ReleaseLab):
    """Graph state never rides an immutable bundle, and an older bundle without the codegraph pin stays selectable."""
    def test_release_manifest_carries_provenance_and_refuses_an_index_in_the_tree(self):
        root, store = self.installation()
        value = self.stage(store)
        manifest = value["manifest"]
        codegraph = manifest["dependencies"]["codegraph"]
        self.assertEqual((codegraph["pin"], codegraph["version"], codegraph["license"], codegraph["path"]), ("1.5.0", sumctl.CODEGRAPH_VERSION, "MIT", ".local/bin/codegraph"))
        self.assertTrue(codegraph["tarball_integrity"].startswith("sha512-"))
        self.assertIn("codegraph", manifest["dependencies"]["tools"]["paths"])
        release = Path(value["release"])
        sumctl.verify_release(release, release.name)
        copy = self.root / "bundle-copy"
        shutil.copytree(release, copy, symlinks=True)
        sumctl.set_read_only(copy, read_only=False)
        (copy / ".codegraph").mkdir()
        with self.assertRaisesRegex(sumctl.SumError, "must not contain a .codegraph index"):
            sumctl.verify_release(copy, release.name)
        shutil.rmtree(copy / ".codegraph")
        manifest_path = copy / sumctl.RELEASE_MANIFEST
        older = json.loads(manifest_path.read_text())
        del older["dependencies"]["tools"]["paths"]["codegraph"]
        older["dependencies"].pop("codegraph")
        manifest_path.write_text(json.dumps(older, indent=2) + "\n")
        (copy / ".local" / "bin" / "codegraph").unlink()
        self.assertEqual(sumctl.verify_release(copy, release.name)["dependencies"]["tools"]["paths"].keys(), older["dependencies"]["tools"]["paths"].keys())
        del older["dependencies"]["tools"]["paths"]["herdr"]
        manifest_path.write_text(json.dumps(older, indent=2) + "\n")
        with self.assertRaisesRegex(sumctl.SumError, "lacks the pinned tool herdr"):
            sumctl.verify_release(copy, release.name)


@unittest.skipUnless(os.environ.get("SUM_REAL_CODEGRAPH_BIN"), "set SUM_REAL_CODEGRAPH_BIN to a codegraph 1.5.0 binary to run the real-tool isolation cases")
class RealCodegraphTest(GraphLab):
    """The same isolation facts against the real pinned binary: separate indexes per worktree, ignored trees excluded, nothing global."""
    def setUp(self):
        super().setUp()
        self.real = os.environ["SUM_REAL_CODEGRAPH_BIN"]
        patch = mock.patch.dict(os.environ, {"SUM_CODEGRAPH_BIN": self.real})
        patch.start()
        self.addCleanup(patch.stop)

    def real_status(self, path):
        result = subprocess.run([self.real, "status", "--json", str(path)], capture_output=True, text=True, check=True, env=sumctl.graph_env())
        return json.loads(result.stdout.strip().splitlines()[-1])

    def real_query(self, path, name):
        result = subprocess.run([self.real, "query", name, "-p", str(path), "--json"], capture_output=True, text=True, check=True, env=sumctl.graph_env())
        text = result.stdout
        return json.loads(text[text.index("["):]) if "[" in text else []

    def test_real_binary_keeps_one_index_per_worktree_and_skips_ignored_trees(self):
        (self.repo / ".gitignore").write_text("projects/\n.sum/\n")
        self.commit_py(self.repo, "src/a.py", "def alpha():\n    return 0\n")
        self.git("add", ".gitignore")
        self.git("commit", "-q", "-m", "ignore")
        sumctl.write_settings(self.store, capacity={"per_repository": 2})
        first, second = self.prepare(), self.prepare()
        for task in (first, second):
            record = self.graph(task)
            self.assertEqual((record["state"], record["tool"]["version"]), ("ready", "1.5.0"), record.get("error"))
            self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all", cwd=task["worktree"]), "")
        (Path(first["worktree"]) / "projects" / "o").mkdir(parents=True)
        (Path(first["worktree"]) / "projects" / "o" / "n.py").write_text("def nested_secret():\n    return 9\n")
        self.commit_py(first["worktree"], "src/a.py", "def alpha():\n    return 'first'\n")
        self.commit_py(second["worktree"], "src/a.py", "def alpha():\n    return 'second'\n\ndef beta():\n    return alpha()\n")
        for task in (first, second):
            record = self.graph_init(task)["graph"]
            self.assertEqual(record["state"], "ready", record.get("error"))
            self.assertIn(record["attempts"][-1]["action"], ("sync", "verified"), "the real 1.5.0 catches committed changes up on its own status call; uncommitted edits stay pending until sync")
        self.assertEqual(self.real_query(first["worktree"], "beta"), [])
        self.assertEqual([r["node"]["name"] for r in self.real_query(second["worktree"], "beta")], ["beta"])
        self.assertEqual(self.real_query(first["worktree"], "nested_secret"), [])
        self.assertFalse(self.real_status(self.repo)["initialized"])
        self.assertIsNone(self.real_status(first["worktree"])["worktreeMismatch"])
        self.assertNotEqual(self.real_status(first["worktree"])["indexPath"], self.real_status(second["worktree"])["indexPath"])
        self.assertEqual(sorted(p.name for p in self.home_dir.iterdir() if p.name != ".codegraph"), [])
        again = self.graph_init(first)["graph"]
        self.assertEqual(again["attempts"][-1]["action"], "verified")
        (Path(first["worktree"]) / "src" / "a.py").write_text("def alpha():\n    return 'edited'\n\ndef epsilon():\n    return 5\n")
        live = self.graph_status(first)["live"]
        self.assertEqual((live["freshness"]["state"], live["freshness"]["pending"]["modified"], live["reconcile_needed"]), ("stale", 1, "sync"))
        self.assertEqual(self.real_query(first["worktree"], "epsilon"), [], "an uncommitted edit is not in the index until sync")
        self.assertEqual(self.graph_init(first)["graph"]["attempts"][-1]["action"], "sync")
        self.assertEqual([r["node"]["name"] for r in self.real_query(first["worktree"], "epsilon")], ["epsilon"])
        self.assertFalse(subprocess.run(["pgrep", "-f", f"codegraph.*{first['worktree']}"], capture_output=True).stdout.strip(), "no watcher outlives the command")


spec_fleet = importlib.util.spec_from_file_location("test_fleet", ROOT / "tests/test_fleet.py")
fleet = importlib.util.module_from_spec(spec_fleet)
spec_fleet.loader.exec_module(fleet)


class GraphFleetTest(fleet.FleetLab):
    """Twelve dispatched workers each get their own index through the installed entrypoint, and a staged update plus rollback leave every index in place."""
    def setUp(self):
        super().setUp()
        self.cg_root = self.root / "fake-codegraph"
        patch = mock.patch.dict(os.environ, {"SUM_CODEGRAPH_BIN": str(FAKE), "FAKE_CODEGRAPH_ROOT": str(self.cg_root), "HOME": str(self.root / "home")})
        (self.root / "home").mkdir()
        patch.start()
        self.addCleanup(patch.stop)

    def test_twelve_workers_get_their_own_indexes_through_update_and_rollback(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "12", "--per-repository", "1", env=env)
        tasks = []
        for number in range(12):
            repo = self.project(f"graph-{number:02d}")
            (repo / "mod.py").write_text(f"def symbol_{number:02d}():\n    return {number}\n")
            self.git("add", "mod.py", cwd=repo)
            self.git("commit", "-q", "-m", "module", cwd=repo)
            task, seconds = self.measure(f"dispatch with graph init ({number + 1}/12)", root, store, "dispatch", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
            self.assertEqual((task["status"], task["graph"]["state"], task["graph"]["last_action"]), ("running", "ready", "init"))
            tasks.append(task)
        peak = int((self.cg_root / "peak.txt").read_text())
        self.assertLessEqual(peak, sumctl.GRAPH_SLOTS)
        indexes = {t["id"]: json.loads((Path(t["worktree"]) / ".codegraph" / "meta.json").read_text()) for t in tasks}
        self.assertEqual(len({v["projectPath"] for v in indexes.values()}), 12)
        for task in tasks:
            self.assertFalse((Path(task["repository"]) / ".codegraph").exists())
            self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all", cwd=task["worktree"]), "")
        rows = {r["id"]: r["graph"] for r in self.ctl(root, store, "status", env=env)["tasks"]}
        self.assertEqual(set(rows.values()), {"ready"})
        sha1 = self.commit_upstream(root, "NOTES.md", "update one\n")
        applied = self.apply(store)
        self.assertEqual((applied["changed"], applied["default"]["sha"]), (True, sha1))
        for task in tasks[:3]:
            status = self.ctl(root, store, "graph", "status", task["id"], env=env)
            self.assertEqual((status["recorded"]["state"], status["live"]["freshness"]["state"], status["live"]["reconcile_needed"]), ("ready", "fresh", None))
            again = self.ctl(root, store, "graph", "init", task["id"], env=env)["graph"]
            self.assertEqual(again["attempts"][-1]["action"], "verified")
        rolled = sumctl.update_rollback(store, self.ns(to="checkout"))
        self.assertEqual(rolled["default"]["kind"], "checkout")
        self.assertEqual({t["id"]: json.loads((Path(t["worktree"]) / ".codegraph" / "meta.json").read_text()) for t in tasks}, indexes)
        self.assertEqual([c for c in json.loads((self.cg_root / "calls.jsonl").read_text().splitlines()[0])["args"]][:1], ["--version"])
        self.assertEqual(sorted(p.name for p in (self.root / "home").iterdir()), [])


for _name in dir(core.CoreTest):
    if _name.startswith("test_"):
        for _cls in (GraphTest, GraphRootVerificationTest, RealCodegraphTest):
            setattr(_cls, _name, None)
for _name in dir(fleet.FleetLab):
    if _name.startswith("test_") and _name not in vars(GraphFleetTest):
        setattr(GraphFleetTest, _name, None)


if __name__ == "__main__":
    unittest.main()
