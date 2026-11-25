"""Read-only Git preflight, executed inside an isolated project sandbox."""

import os
import subprocess
import sys
from pathlib import Path


def git(directory, *arguments):
    environment = {
        "PATH": "/usr/bin:/bin",
        "HOME": "/nonexistent",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_OPTIONAL_LOCKS": "0",
        "GIT_TERMINAL_PROMPT": "0",
        "LC_ALL": "C",
    }
    return subprocess.run(
        ["git", "-C", directory, "-c", "core.fsmonitor=false", *arguments],
        env=environment,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        check=True,
        timeout=30,
    ).stdout


def unreadable(error):
    raise error


def main():
    for directory, children, files in os.walk(".", onerror=unreadable):
        if "HEAD" in files and {"objects", "refs"}.issubset(children):
            raise ValueError("Remove or export bare Git repositories before archiving")

        if ".git" not in children and ".git" not in files:
            continue

        if ".git" in children:
            children.remove(".git")

        if git(directory, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none"):
            raise ValueError(f"Commit or discard local changes in {Path(directory)} before archiving")

        if git(directory, "rev-list", "--max-count=1", "HEAD", "--branches", "--tags", "--not", "--remotes"):
            raise ValueError(f"Push local commits in {Path(directory)} before archiving")


try:
    main()
except ValueError as error:
    sys.exit(str(error))
except (OSError, subprocess.SubprocessError):
    sys.exit("Cannot verify that all project Git worktrees are clean")
