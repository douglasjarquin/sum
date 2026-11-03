#!/usr/bin/env python3
import argparse
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile


def run(argv, **kwargs):
    return subprocess.check_output(argv, text=True, **kwargs).strip()


def main():
    parser = argparse.ArgumentParser(description="Bootstrap an approved merged Sum upgrade in this conversation")
    parser.add_argument("--installation", default=".")
    parser.add_argument("--ref", required=True)
    parser.add_argument("--no-fetch", action="store_true")
    parser.add_argument("--approve", action="store_true")
    parser.add_argument("--operation")
    args = parser.parse_args()
    root = Path(args.installation).resolve(strict=True)
    git = ["git", "-C", str(root)]
    if run(git + ["rev-parse", "--show-toplevel"]) != str(root):
        raise SystemExit("upgrade requires the installation's Git root")
    if (root / ".sum/dev.json").exists() or not (root / ".sum/state.json").is_file():
        raise SystemExit("upgrade requires an existing installation, not a developer checkout")
    if os.environ.get("SUM_TASK") or os.environ.get("SUM_ROLE") in ("worker", "reviewer"):
        raise SystemExit("upgrade cannot run from a task identity")
    try:
        branch = run(git + ["symbolic-ref", "refs/remotes/origin/HEAD"])
    except subprocess.CalledProcessError:
        branch = "refs/remotes/origin/main"
    if not branch.startswith("refs/remotes/origin/"):
        raise SystemExit("invalid origin default branch")
    if not args.no_fetch:
        subprocess.check_call(git + ["fetch", "--quiet", "origin", branch.removeprefix("refs/remotes/origin/")])
    sha = run(git + ["rev-parse", "--verify", args.ref + "^{commit}", "--"])
    subprocess.check_call(git + ["merge-base", "--is-ancestor", sha, branch])
    releases = root / ".local/releases"
    for member in (root / ".local", releases):
        if member.is_symlink():
            raise SystemExit(f"bootstrap path must not be a symlink: {member}")
    releases.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".bootstrap-", dir=releases) as scratch:
        archive = Path(scratch) / "source.tar"
        with archive.open("wb") as output:
            subprocess.check_call(git + ["archive", "--format=tar", sha], stdout=output)
        with tarfile.open(archive) as source:
            source.extractall(scratch, filter="data")
        helper = Path(scratch) / ".local/bin/sumctl"
        helper.parent.mkdir(parents=True, exist_ok=True)
        env = dict(os.environ, CGO_ENABLED="0", GOENV="off", SUM_INSTALL_ROOT=str(root))
        for key in tuple(env):
            if key.startswith(("SUM_", "HC_", "HERDR_")) and key not in ("SUM_INSTALL_ROOT", "SUM_STAGE_OFFLINE", "SUM_HC_BIN", "SUM_GO_BIN", "SUM_GH_BIN", "SUM_CODEGRAPH_BIN"):
                del env[key]
        go = env.get("SUM_GO_BIN") or run(["mise", "which", "go"])
        subprocess.check_call([go, "build", "-trimpath", "-buildvcs=false", "-ldflags",
                               "-X github.com/douglasjarquin/sum/go/internal/runtime.BuildSourceRevision=" + sha,
                               "-o", str(helper), "./cmd/sumctl"], cwd=Path(scratch) / "go", env=env)
        command = [str(helper), "--format", "json", "update", "bootstrap", "--ref", sha, "--no-fetch"]
        if args.approve:
            command.append("--approve")
        if args.operation:
            command.extend(["--operation", args.operation])
        subprocess.check_call(command, cwd=root, env=env)


if __name__ == "__main__":
    main()
