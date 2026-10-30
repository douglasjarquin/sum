#!/usr/bin/env python3
"""Run Sum's application lifecycle over published HC with a model-free TUI.

Git and GitHub destinations are private fixtures. The TUI is not real Codex and
this test does not qualify a harness, authorize an installation migration or
publish anything externally.
"""
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

from hc_lab import Lab


def run(args, cwd=None, env=None, timeout=120):
    result = subprocess.run([str(arg) for arg in args], cwd=cwd, env=env, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{args}: {result.stderr[-3000:]}")
    return result.stdout.strip()


def wait_record(directory, address, tag, timeout=90):
    deadline = time.monotonic() + timeout
    path = directory / (address + "-" + tag + ".json")
    while time.monotonic() < deadline:
        if path.is_file():
            result = json.loads(path.read_text())
            if result.get("code", 1):
                raise RuntimeError(f"{address}/{tag}: {result}")
            return result
        failure = directory / (address + "-error.json")
        if failure.exists():
            raise RuntimeError(failure.read_text())
        time.sleep(0.02)
    raise RuntimeError(f"callback {address}/{tag} did not arrive within {timeout}s")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True, help="development source snapshot to stage read-only")
    parser.add_argument("--hc", required=True)
    parser.add_argument("--artifact", type=Path, required=True)
    args = parser.parse_args()
    source = args.source.resolve()
    lab = Lab(args.hc, args.artifact)
    try:
        execute_canary(args, source, lab)
    except BaseException as error:
        if lab.root.exists() and not (lab.root / "evidence.json").exists():
            lab.summary["preflight_error"] = str(error)
            lab._finish(error)
        raise


def execute_canary(args, source, lab):
    runtime = lab.root / "installation"
    shutil.copytree(source, runtime, ignore=shutil.ignore_patterns(".sum", ".local", ".deps", ".git", ".artifacts", ".codegraph", "node_modules", "__pycache__", ".venv", ".aube"))
    helper = runtime / "bin/sumctl"
    helper.rename(helper.with_name("sumctl-wrapper-source"))
    go = Path(os.environ["GOROOT"]) / "bin/go" if os.environ.get("GOROOT") else "go"
    run([go, "build", "-trimpath", "-buildvcs=false", "-o", helper, "./cmd/sumctl"], cwd=runtime / "go", env={**os.environ, "GOTOOLCHAIN": "local"})
    lab.summary["helper_sha256"] = hashlib.sha256(helper.read_bytes()).hexdigest()
    source_digest = hashlib.sha256()
    for path in sorted((runtime / "go").rglob("*")):
        if path.is_file() and (path.suffix == ".go" or path.name in ("go.mod", "go.sum")):
            source_digest.update(str(path.relative_to(runtime)).encode() + b"\x00" + path.read_bytes())
    lab.summary["source_go_sha256"] = source_digest.hexdigest()
    output = lab.root / "callbacks"
    output.mkdir()
    fake = runtime / ".local/bin/codex"
    fake.parent.mkdir(parents=True)
    fake.write_text("#!" + sys.executable + "\n" + (Path(__file__).parents[1] / "tests/fixtures/hc_sum_harness.py").read_text().split("\n", 1)[1])
    fake.chmod(0o700)
    codex_home = lab.root / "codex-home"
    codex_home.mkdir()
    gh_root = lab.root / "github"
    gh_root.mkdir()
    fake_bin = lab.root / "fixture-bin"
    fake_bin.mkdir()
    (fake_bin / "mise").write_text("#!" + sys.executable + "\n" + (runtime / "tests/fixtures/mise.py").read_text().split("\n", 1)[1])
    (fake_bin / "mise").chmod(0o700)
    lab.env.update(PATH=os.pathsep.join((str(fake_bin), str(Path(sys.executable).parent), lab.env["PATH"])),
                   SUM_MISE_BIN=str(fake_bin / "mise"), FAKE_MISE_STOP=str(lab.root), SUM_CODEX_BIN=str(fake), CODEX_HOME=str(codex_home), SUM_CANARY_OUTPUT=str(output),
                   SUM_GH_BIN=str(runtime / "tests/fixtures/gh_attach.py"), FAKE_GH_ROOT=str(gh_root),
                   SUM_INSTALL_ROOT=str(runtime), GIT_AUTHOR_NAME="Sum canary", GIT_AUTHOR_EMAIL="canary@example.invalid",
                   GIT_COMMITTER_NAME="Sum canary", GIT_COMMITTER_EMAIL="canary@example.invalid", SUM_STAGE_OFFLINE="1")
    lab.summary["fixture_python"] = sys.executable
    lab.summary.update(kind="full-sum-application-composition", authenticated_harness=False,
                       github_backend="local strict fake", source=str(source), helper=str(helper), checkpoints=[])
    sys.path.insert(0, str(runtime / "scripts"))
    setup_spec = importlib.util.spec_from_file_location("canary_setup", runtime / "scripts/setup.py")
    setup = importlib.util.module_from_spec(setup_spec)
    setup_spec.loader.exec_module(setup)
    setup.designate(runtime)
    run(["git", "init", "-b", "main", runtime], env=lab.env)
    run(["git", "add", "AGENTS.md", "COORDINATOR.md", "skills", ".agents"], cwd=runtime, env=lab.env)
    run(["git", "commit", "-m", "Stage private canary runtime"], cwd=runtime, env=lab.env)
    repo = lab.root / "project"
    shutil.copytree(runtime / "tests/fixtures/verify/cli", repo)
    shutil.copytree(runtime / ".agents/skills/verify", repo / ".agents/skills/verify")
    feature_map = repo / "docs/features/cli.md"
    feature_map.write_text("\n".join(line for line in feature_map.read_text().splitlines() if "terminal-colors" not in line) + "\n")
    (repo / ".gitignore").write_text(".artifacts/\n__pycache__/\n")
    with (repo / "mise.toml").open("a") as stream:
        stream.write('lint = "python3 -m py_compile hello.py"\n')
    run(["git", "init", "-b", "main", repo], env=lab.env)
    run(["git", "add", "."], cwd=repo, env=lab.env)
    run(["git", "commit", "-m", "Create private greeting fixture"], cwd=repo, env=lab.env)
    lab.env["SUM_CANARY_BASE"] = run(["git", "rev-parse", "HEAD"], cwd=repo, env=lab.env)
    origin = lab.root / "origin.git"
    run(["git", "clone", "--bare", repo, origin], env=lab.env)
    run(["git", "remote", "add", "origin", origin], cwd=repo, env=lab.env)
    brief = lab.root / "approved.md"
    brief.write_text("Approved canary only: add canary-note.txt; ask whether to keep punctuation, apply the recorded answer, verify greeting tests, and report a candidate.\n")
    (gh_root / "github.json").write_text(json.dumps({"repository": "sum-canary/project", "visibility": "PRIVATE", "viewer_permission": "ADMIN", "checks": []}))

    with lab:
        def cli(*command):
            return json.loads(run([helper, "--format", "json", *command], cwd=runtime, env=lab.env))
        started = cli("coordinator", "start", "--socket", str(lab.socket), "--session", lab.session,
                      "--hc-config-home", lab.env["XDG_CONFIG_HOME"], "--harness", "codex")
        coordinator = started["endpoint"]["address"]
        init = wait_record(output, coordinator, "init")
        assert init["result"]["role"] == "coordinator", init
        lab.summary["checkpoints"].append("coordinator-start-and-init-receipt")

        def command(address, tag, *arguments, action=None):
            payload = {"tag": tag, "args": list(arguments)}
            if action:
                payload["action"] = action
            lab.call("terminal.send", terminal=address, text="SUM_CANARY_CMD:" + json.dumps(payload))
            result = wait_record(output, address, tag)
            lab.summary.setdefault("callbacks", {})[address + "-" + tag] = result
            return result

        prepared = command(coordinator, "prepare", "prepare", "--repo", str(repo), "--brief", str(brief), "--harness", "codex", "--approved")["result"]
        task_id = prepared["id"]
        launched = command(coordinator, "start", "start", task_id)["result"]
        worker = launched["worker_endpoint"]["address"]
        worker_init = wait_record(output, worker, "init")
        assert worker_init["result"]["role"] == "worker"
        lab.summary.update(task=task_id, coordinator=coordinator, worker=worker)
        lab.summary["checkpoints"].append("prepare-start-worker-init-receipt")
        question = command(worker, "ask", "ask", task_id, "--key", "punctuation", "--text", "Keep punctuation?")["result"]
        question_id = question["question"]["id"]
        wake = command(coordinator, "wake-question", "wake", "show")["result"]
        current_wake = next(row for row in wake["recipients"] if row["recipient"]["address"] == coordinator)
        assert current_wake["outstanding"] and current_wake["boundary"], wake
        command(coordinator, "consume-question", "wake", "consume", "--boundary", current_wake["boundary"])
        consumed = command(coordinator, "wake-after-consume", "wake", "show")["result"]
        assert not next(row for row in consumed["recipients"] if row["recipient"]["address"] == coordinator)["outstanding"], consumed
        command(coordinator, "answer", "answer", task_id, question_id, "--text", "Yes, keep punctuation.")
        applied = command(worker, "apply", "resolve", task_id, question_id)["result"]
        assert applied["status"] == "applied" and applied["answer"] == "Yes, keep punctuation.", applied
        authored = command(worker, "candidate", action="candidate")
        candidate = authored["candidate"]
        command(worker, "report", "report", task_id, "--text", "Scripted worker added and committed the canary note.", "--handoff", authored["handoff"])
        lab.summary["checkpoints"].append("question-answer-worker-application-and-report")
        reviewer = command(coordinator, "reviewer-launch", "reviewer", "launch", task_id, "--harness", "codex")["result"]["reviewer"]["address"]
        review_init = wait_record(output, reviewer, "init")
        assert review_init["result"]["role"] == "reviewer"
        command(reviewer, "review", "review", task_id, "--verdict", "approve", "--candidate", candidate, "--text", "Independent scripted reviewer inspected the exact candidate and approved the fixture change.")
        verification = command(coordinator, "verify", "verify", task_id, "--candidate", candidate, "--execute")["result"]
        assert verification["evidence"]["result"] == "pass", verification
        assert verification["evidence"]["certifies"] == candidate, verification
        command(coordinator, "lint", "pipeline", "lint", task_id, "--candidate", candidate)
        command(coordinator, "document", "pipeline", "document", task_id, "--candidate", candidate, "--rationale", "Canary note only; greeting behavior and maps unchanged.")
        command(coordinator, "rebase", "pipeline", "rebase", task_id, "--candidate", candidate)
        lab.summary["checkpoints"].append("independent-reviewer-receipt-review-and-coordinator-verification")
        command(coordinator, "push", "pipeline", "push", task_id)
        github = json.loads((gh_root / "github.json").read_text())
        github.update(head_sha=candidate, head_branch=prepared["branch"])
        (gh_root / "github.json").write_text(json.dumps(github))
        command(coordinator, "create-pr", "pipeline", "pr", task_id, "--title", "Private canary fixture")
        command(coordinator, "publish", "pipeline", "publish", task_id)
        lab.summary["checkpoints"].append("local-bare-push-and-fake-github-publication")
        command(worker, "exit", action="exit")
        command(reviewer, "exit", action="exit")
        fake_state = json.loads((gh_root / "github.json").read_text())
        pr = fake_state["pr"]
        pr.update(state="MERGED", merged_at="2026-10-30T00:00:00Z", merge_commit=candidate)
        (gh_root / "github.json").write_text(json.dumps(fake_state))
        command(coordinator, "reconcile-merge", "pr", "reconcile", task_id, "--number", str(pr["number"]), "--repo", "sum-canary/project")
        cleaned = command(coordinator, "cleanup", "cleanup", task_id, "--apply")["result"]
        assert cleaned["state"] == "complete", cleaned
        lab.summary["checkpoints"].append("fake-merge-observation-and-guarded-cleanup")
        lab.summary.update(candidate=candidate, reviewer=reviewer, cleanup=cleaned)
        lab.summary["callbacks"] = {path.name: json.loads(path.read_text()) for path in output.glob("*.json")}
    print("PASS: real published-HC Sum application lifecycle; scripted TUI and fake GitHub only.")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"Sum HC application canary failed: {exc}", file=sys.stderr)
        sys.exit(1)
