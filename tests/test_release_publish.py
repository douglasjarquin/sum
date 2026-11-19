"""Exercise the sum-verify release publisher against a stand-in gh and disposable Git history."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "publish-sum-verify-release.sh"
ASSETS = ("sum-verify-darwin-amd64", "sum-verify-darwin-arm64", "sum-verify-linux-amd64", "sum-verify-linux-arm64", "checksums.txt")
TAG = "sum-verify-v0.1.0"
RELEASE_BRANCH = "release-please--branches--main--components--sum-verify"

# Stand-in gh backed by $FAKE_GH_STATE: {"releases": {tag: {"draft", "assets", "body"}},
# "prs": [[head, title, merge sha]], "fail_uploads": n, "calls": [...]}. It answers only the
# exact calls the publisher makes and fails on anything else.
FAKE_GH = r'''#!/usr/bin/env python3
import json, os, sys
path = os.environ["FAKE_GH_STATE"]
state = json.load(open(path))
args = sys.argv[1:]
state["calls"].append(args)
def save():
    json.dump(state, open(path, "w"))
def release(tag):
    if tag not in state["releases"]:
        save(); sys.exit("release not found")
    return state["releases"][tag]
if args[:2] == ["release", "view"] and args[3:] == ["--json", "isDraft", "--jq", ".isDraft"]:
    print("true" if release(args[2])["draft"] else "false")
elif args[:2] == ["release", "view"] and args[3:] == ["--json", "assets", "--jq", ".assets[].name"]:
    print("\n".join(release(args[2])["assets"]))
elif args[:2] == ["release", "view"] and args[3:] == ["--json", "body", "--jq", ".body"]:
    print(release(args[2])["body"])
elif args[:2] == ["pr", "list"] and args[2:] == ["--state", "merged", "--base", "main", "--head", "''' + RELEASE_BRANCH + r'''", "--limit", "100", "--json", "title,mergeCommit", "--jq", ".[] | [.title, .mergeCommit.oid] | @tsv"]:
    for head, title, sha in state["prs"]:
        if head == "''' + RELEASE_BRANCH + r'''":
            print(title + "\t" + sha)
elif args[:2] == ["release", "upload"] and args[4:] == ["--clobber"]:
    entry = release(args[2])
    if state["fail_uploads"]:
        state["fail_uploads"] -= 1; save(); sys.exit("upload failed: HTTP 502")
    name = os.path.basename(args[3])
    entry["assets"] = [a for a in entry["assets"] if a != name] + [name]
elif args[:2] == ["release", "edit"] and args[3] == "--notes-file" and args[5:] == ["--draft=false"]:
    entry = release(args[2])
    entry["body"] = open(args[4]).read(); entry["draft"] = False
else:
    save(); sys.exit("unexpected gh call: " + " ".join(args))
save()
'''


class ReleasePublisherTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="release-publish-"))
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        bin_dir = self.tmp / "bin"
        bin_dir.mkdir()
        (bin_dir / "gh").write_text(FAKE_GH)
        (bin_dir / "gh").chmod(0o755)
        self.state_path = self.tmp / "gh.json"
        self.repo = self.tmp / "repo"
        self.repo.mkdir()
        self.env = {**os.environ, "PATH": f"{bin_dir}{os.pathsep}{os.environ['PATH']}",
                    "FAKE_GH_STATE": str(self.state_path), "MAIN_REF": "main"}
        self.env.pop("GITHUB_OUTPUT", None)
        self.git("init", "-q", "-b", "main")
        self.old = self.commit("{}", "ci: adopt release-please")
        self.merge = self.commit('{"tools/verify": "0.1.0"}', "chore(main): release sum-verify 0.1.0 (#2)")
        self.dist = self.tmp / "dist"
        self.dist.mkdir()
        for name in ASSETS:
            (self.dist / name).write_text(name)
        self.toolchain = self.tmp / "toolchain.txt"
        self.toolchain.write_text("sum-verify-linux-amd64: go1.27.4\n")
        self.save({"releases": {}, "prs": [], "fail_uploads": 0, "calls": []})

    def git(self, *args):
        return subprocess.run(["git", "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid",
                               "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", *args],
                              cwd=self.repo, check=True, capture_output=True, text=True).stdout.strip()

    def commit(self, manifest, message):
        (self.repo / ".release-please-manifest.json").write_text(manifest + "\n")
        self.git("add", ".")
        self.git("commit", "-q", "-m", message)
        return self.git("rev-parse", "HEAD")

    def save(self, state):
        self.state_path.write_text(json.dumps(state))

    def load(self):
        return json.loads(self.state_path.read_text())

    def approve(self, tag_at=None, draft=True, assets=()):
        """State after release-please tagged the release PR merge and created its draft."""
        self.git("tag", TAG, tag_at or self.merge)
        state = self.load()
        state["releases"][TAG] = {"draft": draft, "assets": list(assets), "body": "## 0.1.0 (2026-11-19)\n\n### Bug Fixes"}
        state["prs"] = [[RELEASE_BRANCH, "chore(main): release sum-verify 0.1.0", self.merge]]
        self.save(state)

    def run_script(self, *args):
        return subprocess.run(["bash", str(SCRIPT), *args], cwd=self.repo, env=self.env, capture_output=True, text=True)

    def resolve(self):
        result = self.run_script("resolve")
        fields = dict(line.split("=", 1) for line in result.stdout.splitlines())
        return result, fields

    def publish(self):
        return self.run_script("publish", TAG, "0.1.0", str(self.dist), str(self.toolchain))

    def mutations(self):
        return [call for call in self.load()["calls"] if call[:2] in (["release", "upload"], ["release", "edit"])]

    def assert_published(self):
        entry = self.load()["releases"][TAG]
        self.assertFalse(entry["draft"])
        self.assertEqual(sorted(entry["assets"]), sorted(ASSETS))
        self.assertIn("### Bug Fixes", entry["body"])
        self.assertIn("Pin with: .agents/skills/verify/scripts/sum-verify pin 0.1.0", entry["body"])
        self.assertIn("go1.27.4", entry["body"])

    def test_no_released_version_does_nothing(self):
        self.git("checkout", "-q", self.old)
        result, fields = self.resolve()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(fields, {"action": "none"})
        self.assertEqual(self.load()["calls"], [])

    def test_waits_for_release_please_to_create_the_draft(self):
        result, fields = self.resolve()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(fields, {"action": "none"})

    def test_rerun_after_upstream_failure_finishes_the_draft(self):
        # release-please created the tag and draft, then failed before reporting
        # release_created, so the label already says tagged and no output exists.
        self.approve()
        result, fields = self.resolve()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(fields, {"action": "publish", "tag": TAG, "version": "0.1.0", "sha": self.merge})
        result = self.publish()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_published()
        # A later full rerun or push validates the finished release and changes nothing.
        before = len(self.mutations())
        result, fields = self.resolve()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(fields["action"], "done")
        self.assertEqual(len(self.mutations()), before)

    def test_rerun_after_upload_failure_replaces_partial_assets(self):
        self.approve()
        state = self.load()
        state["fail_uploads"] = 1
        self.save(state)
        _, fields = self.resolve()
        self.assertEqual(fields["action"], "publish")
        result = self.publish()
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.load()["releases"][TAG]["draft"])
        state = self.load()
        state["releases"][TAG]["assets"] = ["sum-verify-darwin-amd64"]  # partial upload before the failure
        self.save(state)
        result, fields = self.resolve()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(fields["action"], "publish")
        result = self.publish()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_published()

    def test_publishing_an_already_published_release_is_a_no_op(self):
        self.approve(draft=False, assets=ASSETS)
        result = self.publish()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.mutations(), [])

    def test_published_release_missing_assets_fails(self):
        self.approve(draft=False, assets=ASSETS[:2])
        result, _ = self.resolve()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing sum-verify-linux-amd64", result.stderr)

    def test_stale_tag_on_an_older_main_commit_is_refused(self):
        self.approve(tag_at=self.old)
        result, fields = self.resolve()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"points at {self.old}, but its release PR merged as {self.merge}", result.stderr)
        self.assertNotIn("action", fields)
        self.assertEqual(self.mutations(), [])

    def test_draft_without_a_merged_release_pr_is_refused(self):
        self.approve()
        state = self.load()
        state["prs"] = [["feature", "chore(main): release sum-verify 0.1.0", self.merge],
                        [RELEASE_BRANCH, "chore(main): release sum-verify 0.1.1", self.merge]]
        self.save(state)
        result, _ = self.resolve()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"no merged release PR approves {TAG}", result.stderr)

    def test_release_merge_off_main_is_refused(self):
        self.git("checkout", "-q", "-b", "side", self.old)
        side = self.commit('{"tools/verify": "0.1.0"}', "chore(main): release sum-verify 0.1.0")
        self.merge = side
        self.approve()
        result, _ = self.resolve()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("is not on main", result.stderr)


if __name__ == "__main__":
    unittest.main()
