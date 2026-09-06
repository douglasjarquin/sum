"""Evidence publication (issue #35): validated media from comparison manifests lands in one PR's marked block through `gh pr edit --attach`,
receipts per content hash and destination prevent duplicate uploads, prose outside the block survives, concurrent edits are rebased or refused,
and every failure (hash mismatch, path escape, secret-shaped text, old gh, missing permission, edit failure, timeout) leaves local evidence and
the previous PR body intact. GitHub is a strict fake (`tests/fixtures/gh_attach.py`); rendered output is inspected live in a fixture PR."""
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest
import zlib

ROOT = Path(__file__).resolve().parents[1]
PUBLISH = ROOT / ".agents/skills/evidence/scripts/evidence_publish.py"
FAKE_GH = ROOT / "tests/fixtures/gh_attach.py"
sys.path.insert(0, str(ROOT / ".agents/skills/evidence/scripts"))
import evidence_capture  # noqa: E402
import evidence_publish  # noqa: E402

CANDIDATE = "c" * 40
BASE = "b" * 40
PROSE_TOP = "## Summary\n\nHuman prose above the evidence block. It must survive every publish.\n"
PROSE_BOTTOM = "\n## Notes\n\nHuman prose below the block. Also kept byte for byte.\n"


FFMPEG = shutil.which("ffmpeg")
_VIDEOS = {}


def tiny_video(color):
    """A real, probe-able clip when ffmpeg exists (mp4); otherwise webm bytes the publisher accepts by declared type."""
    if color not in _VIDEOS:
        if FFMPEG:
            with tempfile.TemporaryDirectory() as tmp:
                out = Path(tmp) / "clip.mp4"
                subprocess.run([FFMPEG, "-v", "error", "-y", "-f", "lavfi", "-i", f"color=c={color}:s=32x32:d=0.4:r=10", "-pix_fmt", "yuv420p", "-movflags", "+faststart", str(out)], check=True)
                _VIDEOS[color] = ("screencast.mp4", out.read_bytes(), "video/mp4")
        else:
            _VIDEOS[color] = ("screencast.webm", b"\x1aE\xdf\xa3" + color.encode() * 40, "video/webm")
    return _VIDEOS[color]


def png(width, height, rgb):
    raw = b"".join(b"\x00" + bytes(rgb) * width for _ in range(height))
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")


class PublishLab(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory(prefix="sum-publish-")
        self.root = Path(self._tmp.name).resolve()
        self.addCleanup(self._tmp.cleanup)
        self.gh_root = self.root / "fake-gh"
        self.evidence = self.root / "evidence"
        self.publish_dir = self.root / "publish"
        self.receipts = self.root / "receipts.json"
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("SUM_", "HERDR_", "FAKE_", "EVIDENCE_", "VERIFY_"))}
        self.env["FAKE_GH_ROOT"] = str(self.gh_root)
        self.github(body=PROSE_TOP + PROSE_BOTTOM)

    def github(self, body="", **state):
        self.gh_root.mkdir(exist_ok=True)
        value = {"version": "2.100.0", "repository": "douglasjarquin/project", "visibility": "PUBLIC", "viewer_permission": "WRITE",
                 "pr": {"number": 7, "state": "OPEN", "head_sha": CANDIDATE, "body": body}}
        value.update(state)
        (self.gh_root / "github.json").write_text(json.dumps(value))

    def state(self):
        return json.loads((self.gh_root / "github.json").read_text())

    def body(self):
        return self.state()["pr"]["body"]

    def calls(self):
        path = self.gh_root / "calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def edits(self):
        return [c for c in self.calls() if c["args"][:2] == ["pr", "edit"] and c["args"][2:3] != ["--help"]]

    def capture(self, run, scenario, role, sha, media, outcome, kind="bugfix", redaction=0, feature=None):
        directory = self.evidence / run / evidence_capture.safe_name(scenario) / f"{role}-{sha[:12]}"
        directory.mkdir(parents=True, exist_ok=True)
        entries = []
        for name, data, meta in media:
            (directory / name).write_bytes(data)
            entries.append({"file": name, "bytes": len(data), "sha256": evidence_capture.sha256_file(directory / name), **meta})
        record = {"schema": 1, "run": run, "scenario": scenario, "feature": feature or scenario.split(".")[0], "role": role, "kind": kind, "recipe": "browser", "outcome": outcome,
                  "checkout": {"sha": sha}, "assertions": [{"expectation": "Count: 1", "met": outcome == "pass"}], "limitations": [], "media": entries,
                  "redaction": {"patterns": 4, "count": redaction, "labelled": redaction > 0}, "content_hashes": {e["file"]: e["sha256"] for e in entries}}
        (directory / "capture.json").write_text(json.dumps(record))
        return directory.name, record

    def comparison(self, run, scenario, before, after, verdict="red-green", label="the base fails the user path and the candidate passes it", kind="bugfix", **extra):
        def side(entry):
            if not entry:
                return None
            name, record = entry
            return {"dir": name, "outcome": record["outcome"], "sha": record["checkout"]["sha"], "recipe": "browser", "kind": kind, "assertions": record["assertions"],
                    "limitations": record["limitations"], "blocked_reason": None,
                    "media": [{k: m.get(k) for k in ("file", "type", "sha256", "bytes", "width", "height", "frames", "duration_ms", "role", "derived")} for m in record["media"]]}
        value = {"schema": 1, "run": run, "scenario": scenario, "base": {"sha": BASE}, "candidate": {"sha": CANDIDATE}, "before": side(before), "after": side(after), "findings": [],
                 "verdict": verdict, "label": label, "kind": kind, "visual_proof": "captured", "proves_claim": verdict in ("red-green", "before-after", "after-only"),
                 "outcomes": {"before": before[1]["outcome"] if before else "missing", "after": after[1]["outcome"]}}
        value.update(extra)
        directory = self.evidence / run / evidence_capture.safe_name(scenario)
        (directory / "comparison.json").write_text(json.dumps(value))
        return directory

    def scenario_pair(self, run="run-1", scenario="counter.click", video=True, before=True):
        before_media = [("screenshot.png", png(40, 60, (220, 40, 40)), {"type": "image/png", "width": 40, "height": 60, "role": "screenshot", "derived": False})]
        after_media = [("screenshot.png", png(80, 60, (40, 80, 220)), {"type": "image/png", "width": 80, "height": 60, "role": "screenshot", "derived": False})]
        if video:
            for media, color, avi in ((before_media, "red", b"\0"), (after_media, "blue", b"\1")):
                name, data, mime = tiny_video(color)
                media.append(("screencast.avi", b"RIFF" + avi * 64, {"type": "video/x-msvideo", "role": "screencast", "derived": False, "frames": 4, "duration_ms": 400}))
                media.append((name, data, {"type": mime, "role": "screencast-converted", "derived": True, "frames": 4, "duration_ms": 400}))
        b = self.capture(run, scenario, "before", BASE, before_media, "fail") if before else None
        a = self.capture(run, scenario, "after", CANDIDATE, after_media, "pass")
        if before:
            return self.comparison(run, scenario, b, a)
        return self.comparison(run, scenario, None, a, verdict="after-only", label="before-unavailable: no baseline access; this shows the candidate only, not a red/green pair")

    def run_publish(self, *args, check=None, timeout=60):
        result = subprocess.run([sys.executable, str(PUBLISH), *args], env=self.env, text=True, capture_output=True, timeout=timeout)
        if check is not None:
            self.assertEqual(result.returncode, check, result.stdout + result.stderr)
        return result

    def plan(self, run="run-1", *extra, check=0):
        result = self.run_publish("plan", "--run", run, "--repo", "douglasjarquin/project", "--pr", "7", "--candidate", CANDIDATE, "--base", BASE, "--evidence-root", str(self.evidence),
                                  "--publish-dir", str(self.publish_dir), "--verification-run", "20260906T010203Z-abcd", "--json", *extra, check=check)
        return json.loads(result.stdout) if result.stdout.strip().startswith("{") else result

    def publish(self, *extra, check=None, visibility="public", timeout=60):
        result = self.run_publish("publish", "--plan", str(self.publish_dir / "plan.json"), "--receipts", str(self.receipts), "--visibility", visibility, "--gh", str(FAKE_GH),
                                  "--timeout", "3", "--json", *extra, check=check, timeout=timeout)
        return json.loads(result.stdout) if result.stdout.strip().startswith("{") else result

    def block(self, body=None):
        body = self.body() if body is None else body
        span = evidence_publish.find_block(body)
        return body[span[0]:span[1]] if span else None


class PublishTest(PublishLab):
    def test_capabilities_read_attach_support_from_the_binary(self):
        result = self.run_publish("capabilities", "--gh", str(FAKE_GH), "--json", check=0)
        info = json.loads(result.stdout)
        self.assertEqual((info["version"], info["attach"], info["authenticated"]), ("2.100.0", True, True))
        self.github(version="2.78.0")
        result = self.run_publish("capabilities", "--gh", str(FAKE_GH), "--json", check=2)
        info = json.loads(result.stdout)
        self.assertEqual((info["version"], info["attach"]), ("2.78.0", False))
        self.assertIn("2.99.0+", info["reason"])

    def test_plan_validates_media_and_stages_publish_copies_without_touching_github(self):
        self.scenario_pair()
        plan = self.plan()
        self.assertEqual(plan["publishable_scenarios"], ["counter.click"])
        roles = sorted((m["role"], m["kind"]) for m in plan["scenarios"][0]["media"])
        self.assertEqual(roles, [("after", "image"), ("after", "video"), ("before", "image"), ("before", "video")])
        for m in plan["scenarios"][0]["media"]:
            copy = self.publish_dir / "media" / m["publish_copy"]
            self.assertTrue(copy.is_file())
            self.assertEqual(evidence_capture.sha256_file(copy), m["sha256"])
            self.assertNotIn(" ", m["publish_copy"])
        self.assertTrue((self.evidence / "run-1/counter.click" / f"before-{BASE[:12]}" / "screenshot.png").is_file(), "originals stay in place")
        self.assertIn("<!-- before-and-after:start -->", plan["block_preview"])
        self.assertIn("| Before | After |", plan["block_preview"])
        self.assertIn("not the coordinator's root verification", plan["block_preview"])
        self.assertIn("20260906T010203Z-abcd", plan["block_preview"])
        self.assertEqual(self.calls(), [])

    def test_first_publish_uploads_replaces_nothing_else_and_records_receipts(self):
        self.scenario_pair()
        self.plan()
        result = self.publish(check=0)
        self.assertEqual(result["outcome"], "published", result)
        self.assertEqual(len(result["uploaded"]), 4)
        self.assertEqual(result["reused"], [])
        self.assertEqual(result["video_table"], "applied")
        body = self.body()
        self.assertTrue(body.startswith(PROSE_TOP), body[:200])
        self.assertEqual(body.count("<!-- before-and-after:start -->"), 1)
        self.assertNotIn("](./", body)
        block = self.block()
        self.assertTrue(body.startswith(PROSE_TOP + PROSE_BOTTOM), "a first publish appends after every byte of the existing prose")
        self.assertIn("| Before | After |", block)
        self.assertEqual(block.count("https://github.com/user-attachments/assets/"), 4)
        self.assertIn('<video src="https://github.com/user-attachments/assets/', block)
        self.assertIn("<th>Before (screencast)</th>", block)
        self.assertIn("counter.click", block)
        self.assertIn(f"candidate `{CANDIDATE[:12]}`", block)
        receipts = json.loads(self.receipts.read_text())
        self.assertEqual(len(receipts["attachments"]), 4)
        for key, entry in receipts["attachments"].items():
            self.assertTrue(key.endswith("@douglasjarquin/project#7"))
            self.assertTrue(entry["url"].startswith("https://github.com/user-attachments/assets/"))
        self.assertEqual(len(receipts["blocks"]), 2)
        self.assertTrue(Path(result["record"]).is_file())

    def test_second_publish_reuses_every_attachment_and_replaces_the_same_block_in_place(self):
        self.scenario_pair()
        self.plan()
        first = self.publish(check=0)
        body_after_first = self.body()
        edits_after_first = len(self.edits())
        again = self.publish(check=0)
        self.assertEqual(again["outcome"], "unchanged", again)
        self.assertEqual(len(self.edits()), edits_after_first)
        self.assertEqual(self.body(), body_after_first)
        # A second run with the same media plus a reviewer's edit elsewhere: receipts reuse the URLs, the block is replaced where it stands.
        edited = body_after_first.replace("Human prose above", "Human prose (edited by a reviewer) above")
        state = self.state()
        state["pr"]["body"] = edited
        (self.gh_root / "github.json").write_text(json.dumps(state))
        self.scenario_pair(run="run-2")
        self.plan("run-2")
        result = self.publish(check=0)
        self.assertEqual(result["outcome"], "published", result)
        self.assertEqual(result["uploaded"], [])
        self.assertEqual(len(result["reused"]), 4)
        attach_calls = [c for c in self.edits() if "--attach" in c["args"]]
        self.assertEqual(len(attach_calls), 1, "the second run must not upload anything")
        body = self.body()
        self.assertIn("Human prose (edited by a reviewer) above", body)
        self.assertEqual(body.count("<!-- before-and-after:start -->"), 1)
        self.assertEqual(body.index("<!-- before-and-after:start -->"), edited.index("<!-- before-and-after:start -->"))
        self.assertIn("run=run-2", self.block())
        self.assertEqual(set(evidence_publish.ATTACHMENT_URL.findall(self.block())), set(evidence_publish.ATTACHMENT_URL.findall(self.block(body_after_first))))
        self.assertEqual(len(first["uploaded"]), 4)

    def test_concurrent_outside_edit_is_rebased_and_kept(self):
        self.scenario_pair()
        self.plan()
        self.github(body=PROSE_TOP + PROSE_BOTTOM, mutation={"after_views": 1, "where": "outside", "text": "A reviewer added this paragraph meanwhile."})
        result = self.publish(check=0)
        self.assertEqual(result["outcome"], "published", result)
        body = self.body()
        self.assertIn("A reviewer added this paragraph meanwhile.", body)
        self.assertTrue(body.startswith(PROSE_TOP))
        self.assertEqual(body.count("<!-- before-and-after:start -->"), 1)

    def test_pre_existing_foreign_block_is_refused_until_explicitly_replaced(self):
        foreign = "<!-- before-and-after:start -->\n| Before | After |\n|:---:|:---:|\n| ![b](https://github.com/user-attachments/assets/aaa) | ![a](https://github.com/user-attachments/assets/bbb) |\n<!-- before-and-after:end -->\n"
        self.github(body=PROSE_TOP + "\n" + foreign + PROSE_BOTTOM)
        self.scenario_pair()
        self.plan()
        result = self.publish(check=1)
        self.assertEqual(result["outcome"], "refused", result)
        self.assertIn("did not write", result["reason"])
        self.assertEqual(self.edits(), [])
        self.assertEqual(self.body(), PROSE_TOP + "\n" + foreign + PROSE_BOTTOM)
        result = self.publish("--replace-foreign-block", check=0)
        self.assertEqual(result["outcome"], "published", result)
        body = self.body()
        self.assertTrue(body.startswith(PROSE_TOP + "\n<!-- before-and-after:start -->"))
        self.assertTrue(body.endswith(PROSE_BOTTOM))
        self.assertEqual(body.count("<!-- before-and-after:start -->"), 1)
        self.assertNotIn("assets/aaa", body)

    def test_concurrent_inside_block_edit_is_refused_not_overwritten(self):
        self.scenario_pair()
        self.plan()
        self.publish(check=0)
        self.github(body=self.body(), mutation={"after_views": 1, "where": "inside", "text": "Reviewer: this screenshot looks wrong."})
        self.scenario_pair(run="run-2")
        self.plan("run-2")
        result = self.publish(check=1)
        self.assertEqual(result["outcome"], "refused", result)
        self.assertIn("did not write", result["reason"])
        self.assertIn("Reviewer: this screenshot looks wrong.", self.body())
        self.assertEqual(len([c for c in self.edits()]), 2)

    def test_absent_baseline_publishes_a_labelled_preview_only(self):
        self.scenario_pair(before=False)
        self.plan()
        result = self.publish(check=0)
        self.assertEqual(result["outcome"], "published", result)
        block = self.block()
        self.assertIn("| Preview (after only) |", block)
        self.assertIn("after-only", block)
        self.assertIn("before-unavailable", block)
        self.assertNotIn("| Before | After |", block)
        self.assertIn("<th>Preview (screencast)</th>", block)

    def test_hash_mismatch_and_path_escape_are_refused_before_any_upload(self):
        directory = self.scenario_pair()
        shot = self.evidence / "run-1/counter.click" / f"after-{CANDIDATE[:12]}" / "screenshot.png"
        shot.write_bytes(png(80, 60, (0, 0, 0)))
        plan = self.plan(check=1)
        self.assertEqual(plan["publishable_scenarios"], [])
        self.assertTrue(any("hash" in f and "altered" in f for f in plan["refused_scenarios"]["counter.click"]), plan["refused_scenarios"])
        self.assertFalse((self.publish_dir / "media").exists() and list((self.publish_dir / "media").glob("*after*")))
        result = self.publish(check=1)
        self.assertEqual(result["outcome"], "refused")
        self.assertEqual(self.edits(), [])
        # A manifest pointing outside the run through a symlink or a parent component is refused, and the file is never read into a copy.
        secret = self.root / "outside.png"
        secret.write_bytes(png(8, 8, (1, 2, 3)))
        comparison = json.loads((directory / "comparison.json").read_text())
        comparison["after"]["media"][0]["file"] = "../../../outside.png"
        (directory / "comparison.json").write_text(json.dumps(comparison))
        plan = self.plan(check=1)
        self.assertTrue(any("unsafe path" in f for f in plan["refused_scenarios"]["counter.click"]), plan["refused_scenarios"])
        link_dir = self.evidence / "run-1/counter.click" / f"after-{CANDIDATE[:12]}"
        (link_dir / "linked.png").symlink_to(secret)
        comparison["after"]["media"][0]["file"] = "linked.png"
        comparison["after"]["media"][0]["sha256"] = evidence_capture.sha256_file(secret)
        (directory / "comparison.json").write_text(json.dumps(comparison))
        plan = self.plan(check=1)
        self.assertTrue(any("escapes" in f for f in plan["refused_scenarios"]["counter.click"]), plan["refused_scenarios"])
        self.assertEqual(self.body(), PROSE_TOP + PROSE_BOTTOM)

    def test_secret_shaped_text_or_redacted_capture_refuses_publication(self):
        directory = self.scenario_pair()
        comparison = json.loads((directory / "comparison.json").read_text())
        comparison["label"] = "passes with token=ghp_abcdefghijklmnopqrstuvwxyz0123456789"
        (directory / "comparison.json").write_text(json.dumps(comparison))
        plan = self.plan(check=1)
        self.assertTrue(any("secret-shaped" in f for f in plan["refused_scenarios"]["counter.click"]), plan["refused_scenarios"])
        self.assertFalse(list(self.publish_dir.glob("media/*")) if (self.publish_dir / "media").exists() else [])
        self.scenario_pair(run="run-2")
        after_dir = self.evidence / "run-2/counter.click" / f"after-{CANDIDATE[:12]}"
        record = json.loads((after_dir / "capture.json").read_text())
        record["redaction"] = {"patterns": 4, "count": 1, "labelled": True}
        (after_dir / "capture.json").write_text(json.dumps(record))
        plan = self.plan("run-2", check=1)
        self.assertTrue(any("cannot be redacted" in f for f in plan["refused_scenarios"]["counter.click"]), plan["refused_scenarios"])
        self.assertEqual(self.edits(), [])

    def test_old_gh_defers_publication_and_touches_nothing(self):
        self.scenario_pair()
        self.plan()
        self.github(body=PROSE_TOP + PROSE_BOTTOM, version="2.78.0")
        result = self.publish(check=2)
        self.assertEqual(result["outcome"], "deferred", result)
        self.assertIn("2.99.0+", result["reason"])
        self.assertEqual(self.edits(), [])
        self.assertEqual(self.body(), PROSE_TOP + PROSE_BOTTOM)
        self.assertFalse(self.receipts.exists() and json.loads(self.receipts.read_text())["attachments"])
        self.assertTrue((self.publish_dir / "media").is_dir(), "local publish copies stay available for a later publication")

    def test_private_repository_without_write_permission_or_wrong_visibility_is_refused(self):
        self.scenario_pair()
        self.plan()
        self.github(body=PROSE_TOP + PROSE_BOTTOM, visibility="PRIVATE", viewer_permission="READ")
        result = self.publish(visibility="public", check=1)
        self.assertIn("is private; --visibility public was declared", result["reason"])
        result = self.publish(visibility="private", check=1)
        self.assertIn("READ permission", result["reason"])
        self.assertEqual(self.edits(), [])
        self.github(body=PROSE_TOP + PROSE_BOTTOM, visibility="PRIVATE", viewer_permission="WRITE")
        result = self.publish(visibility="private", check=0)
        self.assertEqual(result["outcome"], "published", result)

    def test_edit_failure_leaves_body_intact_and_reports_unpublished(self):
        self.scenario_pair()
        self.plan()
        self.github(body=PROSE_TOP + PROSE_BOTTOM, edit="exit")
        result = self.publish(check=1)
        self.assertEqual(result["outcome"], "failed", result)
        self.assertIn("intact", result["reason"])
        self.assertEqual(self.body(), PROSE_TOP + PROSE_BOTTOM)
        self.assertEqual(result["uploaded"], [])
        self.assertTrue(all((self.publish_dir / "media" / m["publish_copy"]).is_file() for s in json.loads((self.publish_dir / "plan.json").read_text())["scenarios"] for m in s["media"]))

    def test_partial_upload_restores_the_previous_body_and_keeps_receipts_for_reuse(self):
        self.scenario_pair()
        self.plan()
        self.github(body=PROSE_TOP + PROSE_BOTTOM, edit="partial")
        result = self.publish(check=1)
        self.assertEqual(result["outcome"], "failed", result)
        self.assertTrue(result["restored"])
        self.assertEqual(self.body(), PROSE_TOP + PROSE_BOTTOM)
        self.assertEqual(len(result["uploaded"]), 1)
        receipts = json.loads(self.receipts.read_text())
        self.assertEqual(len(receipts["attachments"]), 1)
        self.github(body=PROSE_TOP + PROSE_BOTTOM, edit="ok")
        result = self.publish(check=0)
        self.assertEqual(result["outcome"], "published")
        self.assertEqual((len(result["uploaded"]), len(result["reused"])), (3, 1))

    def test_timeout_before_any_write_is_reported_failed_with_body_intact(self):
        self.scenario_pair()
        self.plan()
        self.github(body=PROSE_TOP + PROSE_BOTTOM, edit="hang")
        result = self.publish(check=1, timeout=120)
        self.assertEqual(result["outcome"], "failed", result)
        self.assertIn("did not finish", result["reason"])
        self.assertEqual(self.body(), PROSE_TOP + PROSE_BOTTOM)

    def test_timeout_after_a_write_is_reconciled_from_remote_state(self):
        self.scenario_pair()
        self.plan()
        self.github(body=PROSE_TOP + PROSE_BOTTOM, edit="hang-after-write")
        result = self.publish(check=0, timeout=120)
        self.assertEqual(result["outcome"], "published", result)
        self.assertEqual(len(result["uploaded"]), 4)
        receipts = json.loads(self.receipts.read_text())
        self.assertEqual([e["kind"] for e in receipts["events"]][:1], ["edit-attempt"])
        self.assertIn("published", [e["kind"] for e in receipts["events"]])
        self.assertTrue(self.body().startswith(PROSE_TOP))

    def test_head_moved_past_candidate_is_refused_unless_labelled(self):
        self.scenario_pair()
        self.plan()
        self.github(body=PROSE_TOP + PROSE_BOTTOM, pr={"number": 7, "state": "OPEN", "head_sha": "d" * 40, "body": PROSE_TOP + PROSE_BOTTOM})
        result = self.publish(check=1)
        self.assertIn("is not the candidate", result["reason"])
        self.assertEqual(self.edits(), [])
        result = self.publish("--allow-head-mismatch", check=0)
        self.assertEqual(result["outcome"], "published")

    def test_avi_only_screencast_publishes_screenshots_and_names_the_video_as_unsupported(self):
        run, scenario = "run-1", "counter.click"
        b = self.capture(run, scenario, "before", BASE, [("screenshot.png", png(4, 4, (1, 1, 1)), {"type": "image/png", "width": 4, "height": 4, "role": "screenshot", "derived": False}),
                                                        ("screencast.avi", b"RIFF" + b"\0" * 20, {"type": "video/x-msvideo", "role": "screencast", "derived": False, "frames": 3})], "fail")
        a = self.capture(run, scenario, "after", CANDIDATE, [("screenshot.png", png(4, 4, (2, 2, 2)), {"type": "image/png", "width": 4, "height": 4, "role": "screenshot", "derived": False}),
                                                             ("screencast.avi", b"RIFF" + b"\1" * 20, {"type": "video/x-msvideo", "role": "screencast", "derived": False, "frames": 3})], "pass")
        self.comparison(run, scenario, b, a)
        plan = self.plan(check=1)
        self.assertEqual(plan["publishable_scenarios"], [])
        self.assertTrue(any("GitHub renders mp4, mov, and webm only" in f for f in plan["refused_scenarios"]["counter.click"]), plan["refused_scenarios"])

    def test_dry_run_computes_everything_and_edits_nothing(self):
        self.scenario_pair()
        self.plan()
        result = self.publish("--dry-run", check=0)
        self.assertEqual(result["outcome"], "planned")
        self.assertEqual(len(result["to_upload"]), 4)
        self.assertIn("<!-- before-and-after:start -->", result["body_preview"])
        self.assertEqual(self.edits(), [])
        self.assertEqual(self.body(), PROSE_TOP + PROSE_BOTTOM)

    def test_block_command_renders_without_github(self):
        self.scenario_pair()
        self.plan()
        result = self.run_publish("block", "--plan", str(self.publish_dir / "plan.json"), check=0)
        self.assertTrue(result.stdout.startswith("<!-- before-and-after:start -->"))
        self.assertIn("](./counter.click--after--", result.stdout)
        listing = self.run_publish("block", "--plan", str(self.publish_dir / "plan.json"), "--attach-list", check=0)
        self.assertEqual(len(listing.stdout.split()), 4)
        self.assertEqual(self.calls(), [])

    def test_markers_and_splice_keep_every_other_byte(self):
        body = "top\r\n\r\n<!-- before-and-after:start -->\r\nold\r\n<!-- before-and-after:end -->\r\n\r\nbottom\r\n"
        span = evidence_publish.find_block(body)
        out = evidence_publish.splice(body, span, "<!-- before-and-after:start -->\nnew\n<!-- before-and-after:end -->\n")
        self.assertEqual(out, "top\r\n\r\n<!-- before-and-after:start -->\nnew\n<!-- before-and-after:end -->\r\n\r\nbottom\r\n")
        self.assertEqual(evidence_publish.splice("prose\n", None, "BLOCK\n"), "prose\n\nBLOCK\n")
        with self.assertRaises(evidence_publish.Refused):
            evidence_publish.find_block("<!-- before-and-after:start -->\nno end")
        with self.assertRaises(evidence_publish.Refused):
            evidence_publish.find_block("<!-- before-and-after:start -->\n<!-- before-and-after:end -->\n<!-- before-and-after:start -->\n<!-- before-and-after:end -->")
        harvested = evidence_publish.harvest_urls("<!-- sum-media: before=aaa after=bbb -->\n| ![b](https://github.com/user-attachments/assets/1) | ![a](https://github.com/user-attachments/assets/2) |\n"
                                                  "<!-- sum-media: after=ccc -->\nhttps://github.com/user-attachments/assets/3\n")
        self.assertEqual(harvested, {"aaa": "https://github.com/user-attachments/assets/1", "bbb": "https://github.com/user-attachments/assets/2", "ccc": "https://github.com/user-attachments/assets/3"})


if __name__ == "__main__":
    unittest.main()
