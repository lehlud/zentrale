"""Prepare configured checkouts and use deployment keys for Git interactions."""

import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile


def git_environment():
    environment = dict(os.environ)
    for name in ("GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE"):
        environment.pop(name, None)
    environment.update(
        GIT_TERMINAL_PROMPT="0",
        GIT_CONFIG_NOSYSTEM="1",
        GIT_CONFIG_GLOBAL=os.devnull,
    )
    return environment


def checkout_exists(source):
    directory = Path(source["directory"])
    if not directory.is_absolute() or directory.resolve() != directory:
        raise ValueError(f"Repository directory must not contain symlinks: {directory}")
    if not directory.exists():
        return False
    if not directory.is_dir():
        raise ValueError(f"Repository directory is not a directory: {directory}")

    if not (directory / ".git").exists():
        if any(directory.iterdir()):
            raise ValueError(
                f"Repository directory is not empty and has no checkout: {directory}. "
                "Existing files were preserved; select an empty project directory."
            )
        return False

    def read_git(*arguments):
        try:
            return subprocess.run(
                ["git", "-C", str(directory), *arguments],
                env=git_environment(),
                check=True,
                capture_output=True,
                text=True,
            ).stdout.strip()
        except subprocess.CalledProcessError as error:
            detail = (error.stderr or str(error)).strip()
            raise ValueError(
                f"Cannot inspect repository {directory}: {detail}"
            ) from None

    root = Path(read_git("rev-parse", "--show-toplevel")).resolve()
    if root != directory:
        raise ValueError(
            f"Repository checkout belongs to a different directory: {directory}"
        )
    origin = read_git("config", "--get", "remote.origin.url")
    if origin.rstrip("/") != source["url"].rstrip("/"):
        raise ValueError(
            f"Repository origin does not match the configured source: {directory}. "
            "Existing files were preserved; check the project repository selection."
        )
    return True


def write_secret(directory, filename, content):
    destination = os.path.join(directory, filename)
    with open(destination, "w") as target:
        target.write(content)
    os.chmod(destination, 0o600)
    return destination


def disable_push(directory):
    subprocess.run(
        [
            "git",
            "-C",
            directory,
            "remote",
            "set-url",
            "--push",
            "origin",
            "DISABLED_READ_ONLY",
        ],
        check=True,
    )
    hook = os.path.join(directory, ".git", "hooks", "pre-push")
    with open(hook, "w") as target:
        target.write('#!/bin/sh\necho "Read-only source" >&2\nexit 1\n')
    os.chmod(hook, 0o700)


def clone_repository(configuration, environment):
    directory = Path(configuration["directory"])
    # Keep a failed or interrupted transfer from looking like a ready checkout.
    # Staging inside the workspace allows renames on the same shared filesystem.
    with tempfile.TemporaryDirectory(
        prefix=".zentrale-clone-", dir=directory
    ) as staging:
        checkout = Path(staging) / "repository"
        subprocess.run(
            [
                "git",
                "-c",
                "core.hooksPath=/dev/null",
                "clone",
                "--",
                configuration["url"],
                str(checkout),
            ],
            env=environment,
            check=True,
        )
        if configuration["readOnly"]:
            disable_push(checkout)
        if any(entry != Path(staging) for entry in directory.iterdir()):
            raise ValueError(
                f"Repository directory changed during checkout: {directory}"
            )
        # Publish Git metadata last. A stopped preparation cannot silently
        # accept a partly populated working tree as a successful initial clone.
        for entry in sorted(checkout.iterdir(), key=lambda entry: entry.name == ".git"):
            entry.rename(directory / entry.name)


def run_git(configuration):
    temporary_directory = tempfile.mkdtemp(
        prefix="git-", dir=configuration["temporaryDirectory"]
    )
    os.chmod(temporary_directory, 0o700)
    try:
        key = write_secret(temporary_directory, "key", configuration["key"])
        known_hosts = write_secret(
            temporary_directory, "known_hosts", configuration["known"]
        )

        environment = git_environment()
        environment["GIT_SSH_COMMAND"] = (
            "ssh -F /dev/null -i "
            + shlex.quote(key)
            + " -o BatchMode=yes -o ConnectTimeout=15 -o IdentitiesOnly=yes"
            + " -o IdentityAgent=none -o StrictHostKeyChecking=yes"
            + " -o GlobalKnownHostsFile=/dev/null -o UserKnownHostsFile="
            + shlex.quote(known_hosts)
        )

        directory = configuration["directory"]
        if configuration["operation"] in ("clone", "prepare"):
            clone_repository(configuration, environment)
        else:
            arguments = ["git", "-C", directory, *configuration["arguments"]]
            subprocess.run(arguments, env=environment, check=True)
        print(
            json.dumps({"directory": directory, "readOnly": configuration["readOnly"]})
        )
    finally:
        shutil.rmtree(temporary_directory)


def main():
    configuration = json.load(sys.stdin)
    if configuration["operation"] == "inspect":
        missing = []
        for source in configuration["sources"]:
            if not checkout_exists(source):
                missing.append(source["id"])
        print(json.dumps(missing))
        return

    if configuration["operation"] in ("clone", "prepare"):
        if checkout_exists(configuration):
            print(
                json.dumps({"directory": configuration["directory"], "existing": True})
            )
        else:
            Path(configuration["directory"]).mkdir(parents=True, exist_ok=True)
            run_git(configuration)
        return

    run_git(configuration)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(f"Repository preparation failed: {error}")
