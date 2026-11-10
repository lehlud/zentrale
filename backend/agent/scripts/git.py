"""Prepare configured checkouts and use deployment keys for Git interactions."""

import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit


# Installed only in a checkout's private Git metadata, never ~/.ssh or the
# working tree. Validate Git's destination before exposing its deployment key.
SSH_WRAPPER = '''import json, os, pathlib, shlex, sys
root = pathlib.Path(__file__).parent
configuration = json.loads((root / "destination.json").read_text())
arguments = sys.argv[1:]
while arguments[:2] == ["-o", "SendEnv=GIT_PROTOCOL"]:
    arguments = arguments[2:]
if arguments[:1] == ["-p"] and len(arguments) >= 2:
    if arguments[1] != configuration["port"]:
        sys.exit("Unexpected repository SSH port")
    arguments = arguments[2:]
elif configuration["port"] != "22":
    sys.exit("Missing repository SSH port")
if len(arguments) != 2 or arguments[0] != configuration["host"]:
    sys.exit("Unexpected repository SSH host")
command = shlex.split(arguments[1])
allowed = ["git-upload-pack"] if configuration["readOnly"] else ["git-upload-pack", "git-receive-pack"]
if len(command) != 2 or command[0] not in allowed or command[1] != configuration["path"]:
    sys.exit("Unexpected repository SSH command")
os.execvp("ssh", ["ssh", "-F", "/dev/null", "-i", str(root / "key"),
    "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-o", "IdentitiesOnly=yes",
    "-o", "IdentityAgent=none", "-o", "StrictHostKeyChecking=yes",
    "-o", "GlobalKnownHostsFile=/dev/null", "-o", "UserKnownHostsFile=" + str(root / "known_hosts"),
    "-p", configuration["port"], "--", configuration["host"], arguments[1]])
'''


def private_directory(directory):
    directory = Path(directory)
    if not directory.is_absolute() or directory.resolve() != directory:
        raise ValueError("Git metadata must not contain symlinks")
    directory.mkdir(mode=0o700, exist_ok=True)
    os.chmod(directory, 0o700)
    return directory


def atomic_secret(directory, name, content):
    descriptor, temporary = tempfile.mkstemp(dir=directory)
    try:
        with os.fdopen(descriptor, "w") as target:
            target.write(content)
        os.replace(temporary, directory / name)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def project_git_state(project, create=True):
    project = Path(project)
    # Projects with only additional sources need metadata outside a root .git.
    candidates = [project / ".zentrale-project-git", project / ".git" / ".zentrale-project-git"]
    for candidate in candidates:
        if candidate.exists() or candidate.is_symlink():
            return private_directory(candidate)
    if not create:
        return None
    parent = project / ".git" if (project / ".git").exists() else project
    return private_directory(parent / ".zentrale-project-git")


def managed_git_directory(project, directory):
    directory = Path(directory)
    if not directory.is_relative_to(Path(project)):
        raise ValueError("Git credentials are outside project")
    metadata = directory / ".git"
    if not metadata.is_dir() or metadata.resolve() != metadata:
        raise ValueError("Git metadata must be a real directory without symlinks")
    if (metadata / "config").is_symlink():
        raise ValueError("Git configuration must not be a symlink")
    return private_directory(metadata / "zentrale-git")


def clear_project_git(project):
    state = project_git_state(project, create=False)
    if state is None:
        return
    manifest = state / "sources.json"
    if manifest.is_symlink():
        raise ValueError("Git credential manifest must not be a symlink")
    directories = json.loads(manifest.read_text()) if manifest.exists() else []
    for directory in directories:
        if not (Path(directory) / ".git").exists():
            continue
        managed = managed_git_directory(project, directory)
        # Keep a fail-closed include rather than falling back to user SSH keys.
        atomic_secret(managed, "config", '[core]\n\tsshCommand = false\n')
        for name in ("key", "known_hosts", "ssh.py", "destination.json"):
            (managed / name).unlink(missing_ok=True)


def configure_project_git(configuration):
    project = configuration["project"]
    sources = configuration["sources"]
    state = project_git_state(project, create=bool(sources))
    if state is None:
        return
    # Publish the inventory first so interrupted setup is cleared on reconnect.
    atomic_secret(state, "sources.json", json.dumps([s["directory"] for s in sources]))
    for source in sources:
        if not checkout_exists(source):
            raise ValueError("Cannot configure a missing repository")
        directory = source["directory"]
        managed = managed_git_directory(project, directory)
        url = source["url"]
        if url.startswith("ssh://"):
            parsed = urlsplit(url)
            destination = {"host": (parsed.username + "@" if parsed.username else "") + parsed.hostname,
                           "port": str(parsed.port or 22), "path": parsed.path}
        elif ":" in url and not url.startswith("/"):
            host, path = url.split(":", 1)
            destination = {"host": host, "port": "22", "path": path}
        else:
            # Local sources have no SSH credential use, but still get identity.
            destination = None

        def config(*arguments):
            return subprocess.run(["git", "-C", directory, "config", *arguments],
                                  env=git_environment(), check=True, capture_output=True, text=True)

        atomic_secret(managed, "config.pending", "")
        def setting(name, value):
            config("--file", str(managed / "config.pending"), name, value)

        setting("core.sshCommand", "false")
        for name, value in source["identity"].items():
            if value:
                setting("user." + name, value)
        if destination:
            destination["readOnly"] = source["readOnly"]
            atomic_secret(managed, "destination.json", json.dumps(destination))
            atomic_secret(managed, "key", source["key"])
            atomic_secret(managed, "known_hosts", source["known"])
            atomic_secret(managed, "ssh.py", SSH_WRAPPER)
            setting("core.sshCommand", "python3 -I " + shlex.quote(str(managed / "ssh.py")))
            setting("ssh.variant", "ssh")
        if source["readOnly"]:
            setting("remote.origin.pushurl", "DISABLED_READ_ONLY")
        os.replace(managed / "config.pending", managed / "config")
        # Includes are exact paths, not a global includeIf that could affect
        # unrelated repositories. Preserve all user-owned configuration/hooks.
        includes = subprocess.run(["git", "-C", directory, "config", "--local", "--get-all", "include.path"],
                                  env=git_environment(), capture_output=True, text=True).stdout.splitlines()
        if str(managed / "config") not in includes:
            config("--local", "--add", "include.path", str(managed / "config"))


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
    if configuration["operation"] == "clear-project-git":
        clear_project_git(configuration["project"])
        return
    if configuration["operation"] == "configure-project-git":
        configure_project_git(configuration)
        return
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
