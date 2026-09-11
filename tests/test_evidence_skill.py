"""Before/after evidence skill (issue #34): a seeded browser bug reproduced at the base and fixed at the candidate with a screenshot pair and a
playable screencast, a CLI defect through transcripts, build/viewport mismatch detection, an unavailable baseline, after-only features, recorder
failure, redaction, concurrent captures, promotion that preserves originals, the runner reporting missing required evidence, and the whole skill
from a plain external clone with sum absent. Media is validated by reading bytes (dimensions, frames, duration), never by file existence alone."""
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "tests/fixtures/evidence"
SKILL = ".agents/skills/evidence/scripts/evidence_capture.py"
RUNNER = ".agents/skills/verify/scripts/verify_run.py"
sys.path.insert(0, str(ROOT / ".agents/skills/evidence/scripts"))
import evidence_capture  # noqa: E402

BROWSER = evidence_capture.find_browser()
NODE_OK = (evidence_capture.node_version()[1] or "v0").startswith("v") and int((evidence_capture.node_version()[1] or "v0")[1:].split(".")[0]) >= 22
BROWSER_REASON = "no Chromium-family browser on this machine (set EVIDENCE_BROWSER); the blocked path is proven instead" if not BROWSER else ("node 22+ required" if not NODE_OK else "")


class EvidenceLab(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory(prefix="sum-evidence-")
        self.root = Path(self._tmp.name).resolve()
        self.addCleanup(self._tmp.cleanup)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "mise").symlink_to(ROOT / "tests/fixtures/mise.py")
        python_dir = str(Path(sys.executable).resolve().parent)
        node_dir = str(Path(shutil.which("node")).resolve().parent) if shutil.which("node") else ""
        ffmpeg_dir = str(Path(shutil.which("ffmpeg")).parent) if shutil.which("ffmpeg") else ""  # Optional converter, used only when the machine has it.
        # An ordinary project's environment: no SUM_*/HERDR_* variables, no sum on PATH; the browser is whatever the machine has.
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("SUM_", "HERDR_", "FAKE_", "MISE_", "EVIDENCE_", "VERIFY_"))}
        self.env.update(PATH=os.pathsep.join([p for p in (str(self.bin), python_dir, node_dir, ffmpeg_dir, "/usr/bin", "/bin") if p]), FAKE_MISE_STOP=str(self.root), MISE_QUIET="1")
        if BROWSER:
            self.env["EVIDENCE_BROWSER"] = BROWSER
        self.servers = []
        self.addCleanup(self.stop_servers)

    # -- helpers -----------------------------------------------------------------------------
    def git(self, repo, *args):
        return subprocess.run(["git", "-C", str(repo), *args], text=True, capture_output=True, check=True).stdout.strip()

    def project(self, name="project", skills=("evidence", "verify")):
        """A Git repository that vendored the skill(s) the way a project would; nothing resolves through sum's checkout."""
        path = self.root / name
        path.mkdir(parents=True)
        for skill in skills:
            shutil.copytree(ROOT / ".agents/skills" / skill, path / ".agents/skills" / skill, ignore=shutil.ignore_patterns("__pycache__"))
        (path / ".gitignore").write_text(".artifacts/\n")
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=path, check=True)
        self.git(path, "config", "user.email", "lab@example.invalid")
        self.git(path, "config", "user.name", "verify lab")
        self.git(path, "add", "-A")
        self.git(path, "commit", "-q", "-m", "project with vendored skills")
        return path

    def seeded_repo(self, name, fixture, filename, bug, fix):
        """The application under test as its own repository: base commit with the seeded defect, candidate commit with the fix, one worktree each."""
        app = self.root / name / "app"
        app.mkdir(parents=True)
        shutil.copy(FIXTURES / fixture / filename, app / filename)
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=app, check=True)
        self.git(app, "config", "user.email", "lab@example.invalid")
        self.git(app, "config", "user.name", "verify lab")
        self.git(app, "add", "-A")
        self.git(app, "commit", "-q", "-m", "base with seeded defect")
        base = self.git(app, "rev-parse", "HEAD")
        source = (app / filename).read_text()
        self.assertIn(bug, source)
        (app / filename).write_text(source.replace(bug, fix))
        self.git(app, "commit", "-qam", "fix")
        candidate = self.git(app, "rev-parse", "HEAD")
        base_dir, cand_dir = app.parent / "base", app.parent / "candidate"
        self.git(app, "worktree", "add", "-q", str(base_dir), base)
        self.git(app, "worktree", "add", "-q", str(cand_dir), candidate)
        return base, candidate, base_dir, cand_dir

    def run_skill(self, repo, *args, cwd=None, env=None, timeout=180):
        result = subprocess.run([sys.executable, str(repo / SKILL), *args], cwd=str(cwd or repo), env={**self.env, **(env or {})}, text=True, capture_output=True, timeout=timeout)
        return result.returncode, result

    def json_out(self, result):
        return json.loads(result.stdout)

    def serve(self, directory):
        proc = subprocess.Popen([sys.executable, "-u", "-m", "http.server", "0", "--bind", "127.0.0.1", "-d", str(directory)], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, env=self.env)
        self.servers.append(proc)
        line = proc.stdout.readline()  # "Serving HTTP on 127.0.0.1 port NNNNN ..."
        port = int(line.split("port")[1].split()[0])
        return f"http://127.0.0.1:{port}/"

    def stop_servers(self):
        for proc in self.servers:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=5)
            proc.stdout.close()
        self.servers = []

    def capture_dirs(self, repo, run, scenario):
        return {p.name.split("-")[0]: p for p in (repo / ".artifacts/evidence" / run / scenario).iterdir() if p.is_dir()}

    def manifest(self, directory):
        return json.loads((directory / "capture.json").read_text())

    def cli_pair(self, repo, run, scenario="greet.hello", kind="nonvisual", roles=("before", "after")):
        base, candidate, base_dir, cand_dir = self.seeded_repo(f"greet-{repo.name}-{run}", "greet", "greet.py", "sys.exit(1)", "sys.exit(0)")
        codes = {}
        for role, checkout, sha in (("before", base_dir, base), ("after", cand_dir, candidate)):
            if role not in roles:
                continue
            code, result = self.run_skill(repo, "capture", "--scenario", scenario, "--role", role, "--kind", kind, "--run", run, "--checkout", str(checkout), "--expect-sha", sha,
                                          "cli", "--expect-exit", "0", "--expect-text", "Hello, Ada!", "--", sys.executable, str(checkout / "greet.py"), "Ada")
            codes[role] = (code, result)
        return base, candidate, codes

    # -- capabilities ----------------------------------------------------------------------------
    def test_capabilities_report_what_this_machine_can_drive_and_never_claim_an_absent_browser(self):
        repo = self.project()
        code, result = self.run_skill(repo, "capabilities", "--json")
        caps = self.json_out(result)
        self.assertEqual(code, 0, result.stderr)
        self.assertEqual(set(caps), {"browser", "cli", "http", "desktop", "video"})
        self.assertTrue(caps["cli"]["supported"] and caps["http"]["supported"])
        self.assertFalse(caps["desktop"]["supported"])
        self.assertEqual(caps["browser"]["supported"], bool(BROWSER and NODE_OK))
        code, result = self.run_skill(repo, "capabilities", "--json", env={"EVIDENCE_BROWSER": str(self.root / "no-such-browser")})
        caps = self.json_out(result)
        self.assertFalse(caps["browser"]["supported"])
        self.assertIn("no Chromium-family browser", caps["browser"]["reason"])

    def test_browser_driver_launches_with_container_safe_flags_and_an_ephemeral_profile(self):
        source = (ROOT / ".agents/skills/evidence/scripts/evidence_browser.mjs").read_text()
        for flag in ("--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage", "--disable-gpu"):
            self.assertIn(flag, source)
        self.assertIn("mkdtemp", source)
        self.assertIn("evidence-profile-", source)
        self.assertIn("--user-data-dir=${profile}", source)

    # -- the real browser path --------------------------------------------------------------------
    @unittest.skipUnless(BROWSER and NODE_OK, BROWSER_REASON)
    def test_seeded_browser_bug_is_red_at_base_and_green_at_candidate_with_screenshots_and_playable_video(self):
        repo = self.project()
        base, candidate, base_dir, cand_dir = self.seeded_repo("counter", "counter", "index.html", '"Count: " + (n - 1)', '"Count: " + n')
        base_url, cand_url = self.serve(base_dir), self.serve(cand_dir)
        run = "run-browser"
        common = ["--viewport", "800x600", "--theme", "light", "--locale", "en-US", "--step", "click=#add", "--step", "wait=400", "--observe", "#count", "--expect-selector", "#count=Count: 1"]
        code, result = self.run_skill(repo, "capture", "--scenario", "counter.add", "--role", "before", "--kind", "bugfix", "--run", run, "--checkout", str(base_dir), "--expect-sha", base,
                                      "browser", "--url", base_url, *common)
        self.assertEqual(code, 1, result.stdout + result.stderr)  # The defect reproduces: the click happened, the count did not follow.
        self.assertIn("UNMET", result.stdout)
        code, result = self.run_skill(repo, "capture", "--scenario", "counter.add", "--role", "after", "--kind", "bugfix", "--run", run, "--checkout", str(cand_dir), "--expect-sha", candidate,
                                      "browser", "--url", cand_url, *common, "--convert")
        self.assertEqual(code, 0, result.stdout + result.stderr)
        dirs = self.capture_dirs(repo, run, "counter.add")
        before, after = self.manifest(dirs["before"]), self.manifest(dirs["after"])
        self.assertEqual((before["outcome"], after["outcome"]), ("fail", "pass"))
        self.assertEqual((before["checkout"]["sha"], after["checkout"]["sha"]), (base, candidate))
        self.assertTrue(before["checkout"]["matches_expected"] and after["checkout"]["matches_expected"])
        self.assertEqual(before["observations"]["selector_text"], "Count: 0")
        self.assertEqual(after["observations"]["selector_text"], "Count: 1")
        self.assertEqual([s["action"] for s in after["steps"]], ["goto", "click", "wait"])  # The triggering action is on record.
        self.assertTrue(all(s["ok"] for s in after["step_results"]))
        self.assertEqual(after["environment"]["viewport"], "800x600")
        self.assertTrue(after["environment"]["browser_version"])
        self.assertEqual(after["environment"]["user_profile"], "none (ephemeral)")
        for directory, record in ((dirs["before"], before), (dirs["after"], after)):
            media = {m["role"]: m for m in record["media"] if m["role"] != "frame"}
            shot = directory / "screenshot.png"
            data = shot.read_bytes()
            self.assertEqual(struct.unpack(">II", data[16:24]), (800, 600))  # Real PNG header, real requested viewport.
            self.assertEqual((media["screenshot"]["width"], media["screenshot"]["height"], media["screenshot"]["sha256"]), (800, 600, evidence_capture.sha256_file(shot)))
            video = directory / "screencast.avi"
            info = evidence_capture.inspect_avi(video.read_bytes())
            self.assertGreater(info["frames"], 0)
            self.assertGreater(info["duration_ms"], 0)
            self.assertEqual(media["screencast"]["frames"], info["frames"])
            self.assertGreaterEqual(media["screencast"]["source_frames"], 1)
            frames = sorted((directory / "frames").glob("frame-*.jpg"))
            self.assertEqual(len(frames), media["screencast"]["source_frames"])
            first = evidence_capture.jpeg_size(frames[0].read_bytes())
            self.assertEqual((first[0], first[0] <= 800 and 0 < first[1] <= 600), (800, True))
            self.assertEqual((info["width"], info["height"]), first)  # The container header is read from the first frame's bytes.
            self.assertIn("held repeats are timing only", media["screencast"]["encoding"])
            code, result = self.run_skill(repo, "inspect", str(video), "--json")
            self.assertEqual((code, self.json_out(result)["frames"]), (0, info["frames"]))
        converted = [m for m in after["media"] if m["role"] == "screencast-converted"]
        ffmpeg_works = subprocess.run(["ffmpeg", "-version"], env=self.env, capture_output=True).returncode == 0 if shutil.which("ffmpeg", path=self.env["PATH"]) else False
        if ffmpeg_works:
            self.assertEqual(len(converted), 1)
            self.assertTrue(converted[0]["derived"] and converted[0]["source"] == "screencast.avi" and converted[0]["tool"].startswith("ffmpeg"))
            self.assertTrue((dirs["after"] / "screencast.avi").is_file())  # The original survives conversion.
        else:
            self.assertTrue(any("ffmpeg" in limitation for limitation in after["limitations"]), after["limitations"])  # No converter: said so, AVI original kept.
        code, result = self.run_skill(repo, "compare", "--scenario", "counter.add", "--run", run, "--base", base, "--candidate", candidate, "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"], comparison["visual_proof"], comparison["proves_claim"]), (0, "red-green", "captured", True), result.stderr)
        self.assertEqual(comparison["findings"], [])
        self.assertEqual((comparison["before"]["sha"], comparison["after"]["sha"]), (base, candidate))
        html = (repo / ".artifacts/evidence" / run / "counter.add/comparison.html").read_text()
        self.assertIn("Before", html) and self.assertIn("After", html)
        self.assertIn("originals are referenced unchanged", html)
        self.assertIn("CSS scaling only", comparison["derived"][0]["transformation"])

    @unittest.skipUnless(BROWSER and NODE_OK, BROWSER_REASON)
    def test_viewport_and_theme_differences_are_a_mismatch_unless_declared_intentional(self):
        repo = self.project()
        base, candidate, base_dir, cand_dir = self.seeded_repo("counter", "counter", "index.html", '"Count: " + (n - 1)', '"Count: " + n')
        base_url, cand_url = self.serve(base_dir), self.serve(cand_dir)
        for run, theme, flags in (("run-mismatch", "light", []), ("run-intentional", "dark", ["--intentional", "viewport=mobile vs desktop is the point of this scenario", "--intentional", "theme=dark mode added"])):
            self.run_skill(repo, "capture", "--scenario", "counter.add", "--role", "before", "--kind", "bugfix", "--run", run, "--checkout", str(base_dir), *flags,
                           "browser", "--url", base_url, "--viewport", "375x600", "--no-video", "--step", "click=#add", "--observe", "#count", "--expect-selector", "#count=Count: 1")
            code, result = self.run_skill(repo, "capture", "--scenario", "counter.add", "--role", "after", "--kind", "bugfix", "--run", run, "--checkout", str(cand_dir),
                                          "browser", "--url", cand_url, "--viewport", "800x600", "--theme", theme, "--no-video", "--step", "click=#add", "--step", "wait-text=Count: 1", "--observe", "#count", "--expect-selector", "#count=Count: 1")
            self.assertEqual(code, 0, result.stdout + result.stderr)
            code, result = self.run_skill(repo, "compare", "--scenario", "counter.add", "--run", run, "--base", base, "--candidate", candidate, "--json")
            comparison = self.json_out(result)
            if run == "run-mismatch":
                self.assertEqual((code, comparison["verdict"]), (2, "mismatch"))
                self.assertTrue(any("viewport differs" in f for f in comparison["findings"]), comparison["findings"])
            else:
                self.assertEqual((code, comparison["verdict"]), (0, "red-green"), comparison["findings"])
                self.assertEqual(set(comparison["intentional_differences"]), {"viewport", "theme"})
        after = self.manifest(self.capture_dirs(repo, "run-intentional", "counter.add")["after"])
        self.assertTrue(after["observations"]["viewport"]["dark"])  # The theme request reached the page for real.
        self.assertFalse((self.capture_dirs(repo, "run-intentional", "counter.add")["after"] / "screencast.avi").exists())  # --no-video records no video and claims none.

    # -- CLI transcripts, redaction ---------------------------------------------------------------------
    def test_cli_defect_is_red_green_through_transcripts_with_visual_proof_not_applicable_and_secrets_redacted(self):
        repo = self.project()
        base, candidate, codes = self.cli_pair(repo, "run-cli")
        self.assertEqual((codes["before"][0], codes["after"][0]), (1, 0), codes["before"][1].stderr + codes["after"][1].stderr)
        dirs = self.capture_dirs(repo, "run-cli", "greet.hello")
        before, after = self.manifest(dirs["before"]), self.manifest(dirs["after"])
        self.assertEqual((before["outcome"], after["outcome"], after["visual_proof"]), ("fail", "pass", "not-applicable"))
        self.assertEqual(before["observations"]["exit"], 1)
        self.assertEqual(before["steps"][0]["command"][-1], "Ada")
        # Redaction: the seeded credential-shaped value never reaches disk; the copy is labelled; environment values are not recorded.
        transcript = dirs["before"] / "transcript.redacted.txt"
        self.assertTrue(transcript.is_file(), sorted(p.name for p in dirs["before"].iterdir()))
        text = transcript.read_text()
        self.assertIn("token=[REDACTED]", text)
        self.assertNotIn("sk-live", text)
        self.assertNotIn("sk-live", json.dumps(before))
        self.assertGreater(before["redaction"]["count"], 0)
        self.assertTrue(before["redaction"]["labelled"])
        self.assertIn("redacted", " ".join(before["limitations"]))
        self.assertIn("PATH", before["environment"]["env_names"])
        self.assertNotIn(str(self.bin), json.dumps(before["environment"]))  # names, never values
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.hello", "--run", "run-cli", "--base", base, "--candidate", candidate, "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"], comparison["visual_proof"]), (0, "red-green", "not-applicable"), comparison)
        self.assertTrue(comparison["proves_claim"])
        # A second capture into the same run and role is refused: evidence is never overwritten.
        code, result = self.run_skill(repo, "capture", "--scenario", "greet.hello", "--role", "after", "--kind", "nonvisual", "--run", "run-cli", "--checkout", str(self.root / "greet-project-run-cli/candidate"),
                                      "cli", "--", sys.executable, "-c", "print('x')")
        self.assertEqual(code, 3)
        self.assertIn("never overwritten", result.stderr)

    def test_extra_redaction_patterns_and_email_redaction_apply_to_http_responses(self):
        repo = self.project()
        server = self.root / "server.py"
        server.write_text("import http.server, sys\nclass H(http.server.BaseHTTPRequestHandler):\n    def do_GET(self):\n        body = b'{\"owner\": \"ada@example.test\", \"ticket\": \"CASE-4711\", \"ok\": true}'\n"
                          "        self.send_response(200); self.send_header('Content-Type', 'application/json'); self.end_headers(); self.wfile.write(body)\n    def log_message(self, *a): pass\n"
                          "s = http.server.HTTPServer(('127.0.0.1', 0), H); print(s.server_address[1], flush=True); s.serve_forever()\n")
        proc = subprocess.Popen([sys.executable, str(server)], stdout=subprocess.PIPE, text=True, env=self.env)
        self.servers.append(proc)
        port = proc.stdout.readline().strip()
        code, result = self.run_skill(repo, "capture", "--scenario", "api.owner", "--role", "after", "--kind", "nonvisual", "--run", "run-http", "--redact", r"(CASE-)\d+",
                                      "http", "--expect-status", "200", "--expect-text", '"ok": true', "GET", f"http://127.0.0.1:{port}/owner")
        self.assertEqual(code, 0, result.stderr)
        directory = self.capture_dirs(repo, "run-http", "api.owner")["after"]
        text = (directory / "response.redacted.txt").read_text()
        self.assertIn("[REDACTED]", text)
        self.assertNotIn("ada@example.test", text)
        self.assertIn("CASE-[REDACTED]", text)
        record = self.manifest(directory)
        self.assertEqual(record["redaction"]["count"], 2)
        self.assertEqual(record["media"][0]["redacted"], True)

    # -- mismatch, tampering, stale builds -----------------------------------------------------------
    def test_wrong_sha_is_a_stale_build_recipe_mismatch_is_flagged_and_an_altered_original_fails_its_hash(self):
        repo = self.project()
        base, candidate, base_dir, cand_dir = self.seeded_repo("greet", "greet", "greet.py", "sys.exit(1)", "sys.exit(0)")
        # The candidate capture taken from the base checkout: blocked before anything runs, with the reason on record.
        code, result = self.run_skill(repo, "capture", "--scenario", "greet.hello", "--role", "after", "--kind", "nonvisual", "--run", "run-stale", "--checkout", str(base_dir), "--expect-sha", candidate,
                                      "cli", "--", sys.executable, str(base_dir / "greet.py"))
        self.assertEqual(code, 2, result.stdout)
        record = self.manifest(self.capture_dirs(repo, "run-stale", "greet.hello")["after"])
        self.assertEqual(record["outcome"], "blocked")
        self.assertIn("build mismatch", record["blocked_reason"])
        self.assertFalse(record["checkout"]["matches_expected"])
        # A comparison with the declared SHAs catches an undeclared capture from another build too.
        self.run_skill(repo, "capture", "--scenario", "greet.hello", "--role", "before", "--kind", "nonvisual", "--run", "run-wrong", "--checkout", str(cand_dir),
                       "cli", "--expect-exit", "0", "--", sys.executable, str(cand_dir / "greet.py"))
        self.run_skill(repo, "capture", "--scenario", "greet.hello", "--role", "after", "--kind", "nonvisual", "--run", "run-wrong", "--checkout", str(cand_dir),
                       "cli", "--expect-exit", "0", "--", sys.executable, str(cand_dir / "greet.py"))
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.hello", "--run", "run-wrong", "--base", base, "--candidate", candidate, "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"]), (2, "mismatch"))
        self.assertTrue(any("before: captured from" in f and "stale or wrong build" in f for f in comparison["findings"]), comparison["findings"])
        # Recipe mismatch: transcripts before, an HTTP response after, is not a comparison.
        self.run_skill(repo, "capture", "--scenario", "greet.hello", "--role", "before", "--kind", "nonvisual", "--run", "run-recipe", "cli", "--expect-exit", "1", "--", sys.executable, str(base_dir / "greet.py"))
        self.run_skill(repo, "capture", "--scenario", "greet.hello", "--role", "after", "--kind", "nonvisual", "--run", "run-recipe", "http", "GET", "http://127.0.0.1:9/nothing")
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.hello", "--run", "run-recipe", "--json")
        comparison = self.json_out(result)
        self.assertEqual(comparison["verdict"], "mismatch")
        self.assertTrue(any(f.startswith("recipe differs") for f in comparison["findings"]), comparison["findings"])
        # Tampering: an edited original no longer matches its recorded hash.
        base, candidate, _ = self.cli_pair(repo, "run-tamper")
        before_dir = self.capture_dirs(repo, "run-tamper", "greet.hello")["before"]
        transcript = next(before_dir.glob("transcript*.txt"))
        transcript.write_text(transcript.read_text().replace("exit 1", "exit 0"))
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.hello", "--run", "run-tamper", "--base", base, "--candidate", candidate, "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"]), (2, "mismatch"))
        self.assertTrue(any("content hash mismatch" in f for f in comparison["findings"]), comparison["findings"])

    # -- honest labels: unavailable baseline, features, base that already passes ------------------------------
    def test_unavailable_baseline_is_after_only_never_an_invented_red(self):
        repo = self.project()
        base, candidate, codes = self.cli_pair(repo, "run-unavail", roles=("after",))
        code, result = self.run_skill(repo, "unavailable", "--scenario", "greet.hello", "--role", "before", "--kind", "nonvisual", "--run", "run-unavail", "--reason", "base build needs a licence server the lab has no access to")
        self.assertEqual(code, 0, result.stderr)
        dirs = self.capture_dirs(repo, "run-unavail", "greet.hello")
        self.assertEqual(self.manifest(dirs["before"])["outcome"], "unavailable")
        self.assertEqual(self.manifest(dirs["before"])["media"], [])  # Nothing was fabricated.
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.hello", "--run", "run-unavail", "--base", base, "--candidate", candidate, "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"]), (1, "after-only"))  # For a bug fix an after-only capture does not prove the fix.
        self.assertTrue(comparison["label"].startswith("before-unavailable: base build needs a licence server"))
        self.assertFalse(comparison["proves_claim"])
        self.assertEqual(comparison["outcomes"], {"before": "unavailable", "after": "pass"})
        # No before capture at all is reported the same way, not passed.
        _, _, _ = self.cli_pair(repo, "run-none", roles=("after",))
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.hello", "--run", "run-none", "--json")
        self.assertEqual((code, self.json_out(result)["verdict"]), (1, "after-only"))

    def test_new_feature_is_labelled_before_after_and_a_fix_that_already_passes_is_not_a_fix(self):
        repo = self.project()
        # A feature whose before state legitimately shows absence: both captures pass their own expectations, honest label, exit 0.
        base, candidate, base_dir, cand_dir = self.seeded_repo("feat", "greet", "greet.py", "sys.exit(1)", "sys.exit(0)")
        self.run_skill(repo, "capture", "--scenario", "greet.exit", "--role", "before", "--kind", "feature", "--run", "run-feat", "--checkout", str(base_dir), "--expect-sha", base,
                       "cli", "--expect-exit", "1", "--", sys.executable, str(base_dir / "greet.py"))
        self.run_skill(repo, "capture", "--scenario", "greet.exit", "--role", "after", "--kind", "feature", "--run", "run-feat", "--checkout", str(cand_dir), "--expect-sha", candidate,
                       "cli", "--expect-exit", "0", "--", sys.executable, str(cand_dir / "greet.py"))
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.exit", "--run", "run-feat", "--base", base, "--candidate", candidate, "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"], comparison["proves_claim"]), (0, "before-after", True), comparison)
        self.assertIn("shows the absence, not a defect", comparison["label"])
        # A feature with no baseline access: after-only proves the feature exists, and says so.
        self.run_skill(repo, "unavailable", "--scenario", "greet.exit", "--role", "before", "--kind", "feature", "--run", "run-feat2", "--reason", "new page; no prior state")
        self.run_skill(repo, "capture", "--scenario", "greet.exit", "--role", "after", "--kind", "feature", "--run", "run-feat2", "--checkout", str(cand_dir), "cli", "--", sys.executable, str(cand_dir / "greet.py"))
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.exit", "--run", "run-feat2", "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"], comparison["proves_claim"]), (0, "after-only", True))
        # A "bug fix" whose base already passes the path is reported as such, not as red/green.
        for role, checkout in (("before", cand_dir), ("after", cand_dir)):
            self.run_skill(repo, "capture", "--scenario", "greet.exit", "--role", role, "--kind", "bugfix", "--run", "run-nofix", "--checkout", str(checkout), "cli", "--", sys.executable, str(checkout / "greet.py"))
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.exit", "--run", "run-nofix", "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"], comparison["proves_claim"]), (1, "before-also-passes", False))
        # A candidate that still fails is never green.
        self.run_skill(repo, "capture", "--scenario", "greet.exit", "--role", "before", "--kind", "bugfix", "--run", "run-still", "--checkout", str(base_dir), "cli", "--", sys.executable, str(base_dir / "greet.py"))
        self.run_skill(repo, "capture", "--scenario", "greet.exit", "--role", "after", "--kind", "bugfix", "--run", "run-still", "--checkout", str(base_dir), "cli", "--", sys.executable, str(base_dir / "greet.py"))
        code, result = self.run_skill(repo, "compare", "--scenario", "greet.exit", "--run", "run-still", "--json")
        self.assertEqual((code, self.json_out(result)["verdict"]), (1, "after-fails"))

    # -- recorder failure -----------------------------------------------------------------------------
    def test_missing_or_crashing_browser_leaves_a_blocked_capture_with_diagnostics_and_never_a_pass(self):
        repo = self.project()
        code, result = self.run_skill(repo, "capture", "--scenario", "counter.add", "--role", "before", "--kind", "bugfix", "--run", "run-nobrowser",
                                      "browser", "--url", "http://127.0.0.1:9/", "--step", "click=#add", env={"EVIDENCE_BROWSER": str(self.root / "absent")})
        self.assertEqual(code, 2, result.stdout + result.stderr)
        record = self.manifest(self.capture_dirs(repo, "run-nobrowser", "counter.add")["before"])
        self.assertEqual(record["outcome"], "blocked")
        self.assertIn("browser recipe unavailable", record["blocked_reason"])
        self.assertEqual([m for m in record["media"] if m["role"] == "screenshot"], [])
        if not NODE_OK:
            self.skipTest("node 22+ is needed to exercise the driver against a crashing browser")
        crashing = self.root / "crashing-browser"
        crashing.write_text("#!/bin/sh\necho 'renderer died on start' >&2\nexit 3\n")
        crashing.chmod(0o755)
        code, result = self.run_skill(repo, "capture", "--scenario", "counter.add", "--role", "after", "--kind", "bugfix", "--run", "run-nobrowser",
                                      "browser", "--url", "http://127.0.0.1:9/", "--step", "click=#add", "--step-timeout", "2", env={"EVIDENCE_BROWSER": str(crashing)})
        self.assertEqual(code, 2, result.stdout + result.stderr)
        directory = self.capture_dirs(repo, "run-nobrowser", "counter.add")["after"]
        record = self.manifest(directory)
        self.assertEqual(record["outcome"], "blocked")
        self.assertIn("browser capture failed", record["blocked_reason"])
        diagnostics = (directory / "browser-diagnostics.log").read_text()
        self.assertIn("renderer died on start", diagnostics)  # The recorder's own words are retained.
        self.assertIn("browser-diagnostics.log", [m["file"] for m in record["media"]])
        self.assertFalse((directory / "screenshot.png").exists())
        code, result = self.run_skill(repo, "compare", "--scenario", "counter.add", "--run", "run-nobrowser", "--json")
        comparison = self.json_out(result)
        self.assertEqual((code, comparison["verdict"], comparison["proves_claim"]), (2, "capture-failed", False))

    # -- concurrency and promotion ------------------------------------------------------------------------
    def test_concurrent_worker_and_root_captures_do_not_interfere(self):
        repo = self.project()
        base, candidate, base_dir, cand_dir = self.seeded_repo("greet", "greet", "greet.py", "sys.exit(1)", "sys.exit(0)")
        jobs = [("run-worker", "before", base_dir), ("run-worker", "after", cand_dir), ("run-root", "before", base_dir), ("run-root", "after", cand_dir)]
        results = {}

        def work(run, role, checkout):
            results[(run, role)] = self.run_skill(repo, "capture", "--scenario", "greet.hello", "--role", role, "--kind", "nonvisual", "--run", run, "--checkout", str(checkout),
                                                  "cli", "--expect-exit", "0", "--", sys.executable, str(checkout / "greet.py"), "Ada")
        threads = [threading.Thread(target=work, args=job) for job in jobs]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual({k: v[0] for k, v in results.items()}, {("run-worker", "before"): 1, ("run-worker", "after"): 0, ("run-root", "before"): 1, ("run-root", "after"): 0})
        for run in ("run-worker", "run-root"):
            dirs = self.capture_dirs(repo, run, "greet.hello")
            self.assertEqual(set(dirs), {"before", "after"})
            code, result = self.run_skill(repo, "compare", "--scenario", "greet.hello", "--run", run, "--base", base, "--candidate", candidate, "--json")
            self.assertEqual((code, self.json_out(result)["verdict"]), (0, "red-green"))
        manifests = [self.manifest(d) for run in ("run-worker", "run-root") for d in self.capture_dirs(repo, run, "greet.hello").values()]
        self.assertEqual(len({m["run"] + m["role"] for m in manifests}), 4)

    def test_promotion_out_of_a_disposable_checkout_preserves_every_original_byte(self):
        repo = self.project()
        base, candidate, _ = self.cli_pair(repo, "run-promote")
        self.run_skill(repo, "compare", "--scenario", "greet.hello", "--run", "run-promote", "--base", base, "--candidate", candidate)
        source = repo / ".artifacts/evidence/run-promote"
        originals = {p.relative_to(source): evidence_capture.sha256_file(p) for p in source.rglob("*") if p.is_file()}
        durable = self.root / "durable"
        code, result = self.run_skill(repo, "promote", "--run", "run-promote", "--to", str(durable), "--json")
        summary = self.json_out(result)
        self.assertEqual((code, summary["hash_failures"]), (0, []), result.stderr)
        self.assertGreater(summary["files_verified"], 0)
        shutil.rmtree(repo)  # The worktree is gone, as after cleanup.
        promoted = durable / "run-promote"
        copies = {p.relative_to(promoted): evidence_capture.sha256_file(p) for p in promoted.rglob("*") if p.is_file() and p.name != "promoted.json"}
        self.assertEqual(copies, originals)
        self.assertTrue((promoted / "greet.hello/comparison.json").is_file())
        # Promotion never overwrites an earlier promotion.
        other = self.project("project2")
        self.cli_pair(other, "run-promote")
        code, result = self.run_skill(other, "promote", "--run", "run-promote", "--to", str(durable))
        self.assertEqual(code, 3)
        self.assertIn("never overwrites", result.stderr)
        # VERIFY_EVIDENCE_ROOT places captures outside the checkout from the start.
        outside = self.root / "outside"
        code, result = self.run_skill(other, "capture", "--scenario", "greet.hello", "--role", "after", "--kind", "nonvisual", "--run", "run-outside", "cli", "--", sys.executable, "-c", "print('ok')",
                                      env={"VERIFY_EVIDENCE_ROOT": str(outside)})
        self.assertEqual(code, 0, result.stderr)
        self.assertTrue((outside / "run-outside/greet.hello").is_dir())
        self.assertEqual(self.manifest(next((outside / "run-outside/greet.hello").iterdir()))["evidence_root"]["declared_by"], "VERIFY_EVIDENCE_ROOT")

    # -- the verify runner reports missing required evidence ------------------------------------------------
    def test_runner_reports_required_visual_evidence_missing_until_a_comparison_for_the_candidate_exists(self):
        repo = self.root / "cli"
        shutil.copytree(ROOT / "tests/fixtures/verify/cli", repo)
        shutil.copytree(ROOT / ".agents/skills/verify", repo / ".agents/skills/verify", ignore=shutil.ignore_patterns("__pycache__"))
        shutil.copytree(ROOT / ".agents/skills/evidence", repo / ".agents/skills/evidence", ignore=shutil.ignore_patterns("__pycache__"))
        cli_map = repo / "docs/features/cli.md"
        cli_map.write_text(cli_map.read_text() + "| `cli.banner` | The greeting banner renders in the terminal | manual: run it and look | screenshot pair (before/after) |\n")
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=repo, check=True)
        self.git(repo, "config", "user.email", "lab@example.invalid")
        self.git(repo, "config", "user.name", "verify lab")
        self.git(repo, "add", "-A")
        self.git(repo, "commit", "-q", "-m", "fixture with a visual row")
        head = self.git(repo, "rev-parse", "HEAD")

        def runner():
            result = subprocess.run([sys.executable, str(repo / RUNNER), "--json", "--base", head], cwd=str(repo), env=self.env, text=True, capture_output=True, timeout=240)
            return result.returncode, json.loads(result.stdout), result
        code, record, result = runner()
        self.assertEqual((code, record["outcome"]), (0, "pass"), result.stderr)
        self.assertEqual(record["evidence"]["required"], ["cli.banner"])
        self.assertEqual(record["evidence"]["missing"], ["cli.banner"])
        row = {s["id"]: s for s in record["scenarios"]}["cli.banner"]
        self.assertEqual(row["status"], "not-run")
        self.assertIn("required visual evidence", row["reason"])
        self.assertTrue(record["certifies"])  # Missing evidence is reported truthfully; the automated rows still certify what they cover.
        self.assertIn("required evidence missing for: cli.banner", subprocess.run([sys.executable, str(repo / RUNNER), "--base", head], cwd=str(repo), env=self.env, text=True, capture_output=True).stdout)
        # A comparison for this candidate satisfies the requirement; one for another SHA does not.
        for run, checkout_sha in (("run-other", "0" * 40), ("run-head", head)):
            for role, exit_code in (("before", 1), ("after", 0)):
                self.run_skill(repo, "capture", "--scenario", "cli.banner", "--role", role, "--kind", "visual", "--run", run, "--checkout", str(repo), "cli", "--expect-exit", "0", "--", sys.executable, "-c", f"raise SystemExit({exit_code})")
            self.run_skill(repo, "compare", "--scenario", "cli.banner", "--run", run, "--candidate", checkout_sha)
        other = json.loads((repo / ".artifacts/evidence/run-other/cli.banner/comparison.json").read_text())
        self.assertEqual(other["verdict"], "mismatch")  # Declared candidate 000... but captured at HEAD: named, not accepted.
        code, record, _ = runner()
        self.assertEqual(record["evidence"]["missing"], [])
        self.assertEqual([p["path"] for p in record["evidence"]["present"]["cli.banner"]], [".artifacts/evidence/run-head/cli.banner/comparison.json"])
        self.assertNotIn("required visual evidence", {s["id"]: s for s in record["scenarios"]}["cli.banner"]["reason"])

    # -- plain external clone, sum absent --------------------------------------------------------------------
    def test_external_clone_runs_the_skill_with_sum_absent(self):
        origin = self.project("origin")
        external = self.root / "elsewhere/clone"
        external.parent.mkdir()
        subprocess.run(["git", "clone", "-q", str(origin), str(external)], check=True)
        shutil.rmtree(origin)
        for path in external.rglob("*"):
            if path.is_file() and ".git" not in path.parts:
                self.assertNotIn(str(ROOT), path.read_text(errors="replace"), path)  # Nothing points back at sum's checkout.
        self.assertFalse(any(k.startswith(("SUM_", "HERDR_")) for k in self.env))
        self.assertIsNone(shutil.which("sumctl", path=self.env["PATH"]))
        code, result = self.run_skill(external, "capabilities", "--json", cwd=external / ".agents")
        self.assertEqual(code, 0, result.stderr)
        base, candidate, codes = self.cli_pair(external, "run-ext")
        self.assertEqual((codes["before"][0], codes["after"][0]), (1, 0))
        code, result = self.run_skill(external, "compare", "--scenario", "greet.hello", "--run", "run-ext", "--base", base, "--candidate", candidate, "--json")
        self.assertEqual((code, self.json_out(result)["verdict"]), (0, "red-green"), result.stderr)
        transcript = next(self.capture_dirs(external, "run-ext", "greet.hello")["after"].glob("transcript*.txt"))
        code, result = self.run_skill(external, "inspect", str(transcript))
        self.assertEqual(code, 0)
        self.assertIn("text/plain", result.stdout)
        code, result = self.run_skill(external, "promote", "--run", "run-ext", "--to", str(self.root / "kept"))
        self.assertEqual(code, 0, result.stderr)
        self.assertEqual(self.git(external, "status", "--porcelain"), "")  # .artifacts/ stays ignored; the clone is untouched.
        # sum's own contract declares the evidence root and lists the skill as policy.
        self.assertIn('evidence = ".artifacts/evidence"', (ROOT / "VERIFY.md").read_text())
        self.assertIn(".agents/skills/evidence/", (ROOT / "VERIFY.md").read_text())

    def test_media_inspection_reads_bytes_and_rejects_empty_or_forged_files(self):
        tiny_png = b"\x89PNG\r\n\x1a\n" + struct.pack(">I", 13) + b"IHDR" + struct.pack(">IIBBBBB", 3, 2, 8, 2, 0, 0, 0) + b"\0\0\0\0"
        self.assertEqual(evidence_capture.png_size(tiny_png), (3, 2))
        with self.assertRaises(ValueError):
            evidence_capture.png_size(b"not a png at all")
        zero = b"\x89PNG\r\n\x1a\n" + struct.pack(">I", 13) + b"IHDR" + struct.pack(">IIBBBBB", 0, 0, 8, 2, 0, 0, 0) + b"\0\0\0\0"
        path = self.root / "zero.png"
        path.write_bytes(zero)
        with self.assertRaises(ValueError):
            evidence_capture.inspect_media(path)  # 0x0 is empty, not evidence.
        with self.assertRaises(ValueError):
            evidence_capture.inspect_avi(b"RIFF\0\0\0\0AVI " + b"junk")
        frame = self.root / "f.jpg"
        # A minimal baseline JPEG header: SOI, SOF0 with 4x5, then EOI. Enough for size parsing, which is what the skill checks.
        frame.write_bytes(b"\xff\xd8" + b"\xff\xc0" + struct.pack(">H", 17) + b"\x08" + struct.pack(">HH", 5, 4) + b"\x03" + b"\x01\x22\x00\x02\x11\x01\x03\x11\x01" + b"\xff\xd9")
        self.assertEqual(evidence_capture.jpeg_size(frame.read_bytes()), (4, 5))
        avi = self.root / "clip.avi"
        total, held = evidence_capture.write_mjpeg_avi([{"path": str(frame), "ms": 0, "width": 4, "height": 5}, {"path": str(frame), "ms": 300, "width": 4, "height": 5}], avi)
        self.assertEqual((total, held), (4, 2))  # 300 ms at 10 fps holds the first frame 3 times, then the last once.
        info = evidence_capture.inspect_avi(avi.read_bytes())
        self.assertEqual((info["frames"], info["width"], info["height"], info["duration_ms"], info["fps"]), (4, 4, 5, 400, 10.0))


if __name__ == "__main__":
    unittest.main()
