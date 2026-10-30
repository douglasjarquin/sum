#!/usr/bin/env python3
import os
from pathlib import Path
import sys
import tempfile


def environment(artifact_root):
    directory = Path(tempfile.mkdtemp(prefix="run-", dir=artifact_root))
    config = directory / "config"
    descriptor = os.open(config, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        stream.write("[user]\n\tname = Sum Test\n\temail = sum-test@example.invalid\n[commit]\n\tgpgSign = false\n[tag]\n\tgpgSign = false\n")
    blocked = {
        "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
        "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE",
        "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
        "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_TEMPLATE_DIR",
    }
    env = {key: value for key, value in os.environ.items()
           if not key.startswith("GIT_CONFIG") and key not in blocked}
    env.update(GIT_CONFIG_GLOBAL=str(config.resolve()), GIT_CONFIG_NOSYSTEM="1")
    return env


def main():
    command = sys.argv[1:]
    if command and command[0] == "--":
        command = command[1:]
    if not command:
        raise SystemExit("usage: test_git_env.py -- COMMAND [ARG ...]")
    artifact_root = Path(__file__).resolve().parents[1] / ".artifacts" / "test-git"
    artifact_root.mkdir(parents=True, exist_ok=True)
    os.execvpe(command[0], command, environment(artifact_root))


if __name__ == "__main__":
    main()
