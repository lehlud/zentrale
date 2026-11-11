"""Prepare configured checkouts and use deployment keys for Git interactions."""

import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from urllib.parse import urlsplit


READ_ONLY_HOOK = b'#!/bin/sh\necho "Read-only source" >&2\nexit 1\n'


# Installed only in a checkout's private Git metadata, never ~/.ssh or the
# working tree. Validate Git's destination before exposing its deployment key.
SSH_WRAPPER = """import json, os, pathlib, shlex, sys
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
    "-o", "GlobalKnownHostsFile=/dev/null", "-o", "UserKnownHostsFile=" + json.dumps(str(root / "known_hosts"), ensure_ascii=False),
    "-p", configuration["port"], "--", configuration["host"], arguments[1]])
"""


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
    candidates = [
        project / ".zentrale-project-git",
        project / ".git" / ".zentrale-project-git",
    ]
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


def ssh_destination(url):
    if url.startswith("ssh://"):
        parsed = urlsplit(url)
        return {
            "host": (parsed.username + "@" if parsed.username else "")
            + parsed.hostname,
            "port": str(parsed.port or 22),
            "path": parsed.path,
        }
    if ":" in url and "://" not in url and not url.startswith("/"):
        host, path = url.split(":", 1)
        return {"host": host, "port": "22", "path": path}
    return None


def normalized_repository_url(url):
    parsed = urlsplit(url)
    if (
        parsed.scheme in ("http", "https")
        and parsed.hostname
        and not parsed.query
        and not parsed.fragment
    ):
        # Credentials in an old HTTPS address do not change its repository.
        # Configuration replaces that address with the selected SSH transport.
        host = parsed.hostname
        default_port = 443 if parsed.scheme == "https" else 80
        if parsed.port and parsed.port != default_port:
            host += ":" + str(parsed.port)
        return (
            (parsed.scheme + "://" + host + parsed.path)
            .rstrip("/")
            .removesuffix(".git")
        )
    return url.rstrip("/")


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
        atomic_secret(managed, "config", "[core]\n\tsshCommand = false\n")
        subprocess.run(
            [
                "git",
                "-C",
                directory,
                "config",
                "--local",
                "--replace-all",
                "remote.origin.pushurl",
                "DISABLED_DETACHED_SOURCE",
            ],
            env=git_environment(),
            check=True,
            capture_output=True,
        )
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
        cleanup_finished_clones(source)
        directory = source["directory"]
        managed = managed_git_directory(project, directory)
        url = source["url"]
        destination = ssh_destination(url)

        def config(*arguments):
            return subprocess.run(
                ["git", "-C", directory, "config", *arguments],
                env=git_environment(),
                check=True,
                capture_output=True,
                text=True,
            )

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
            setting(
                "core.sshCommand", "python3 -I " + shlex.quote(str(managed / "ssh.py"))
            )
            setting("ssh.variant", "ssh")

            # Select the repository key even when a terminal/extension supplies
            # its HTTPS clone URL or the user's global config rewrites SSH URLs.
            # Full-repository prefixes take precedence over host-wide rewrites.
            aliases = repository_url_aliases(
                url, destination, source.get("httpUrls", [])
            )
            for alias in aliases:
                config(
                    "--file",
                    str(managed / "config.pending"),
                    "--add",
                    "url." + url + ".insteadOf",
                    alias,
                )

        # pushurl is multi-valued: adding another include cannot override an
        # old HTTPS target. Replace origin's local list with the selected source.
        # This also repairs read-only -> writable transitions on reused checkouts.
        push_url = "DISABLED_READ_ONLY" if source["readOnly"] else url
        if destination:
            config("--local", "--replace-all", "remote.origin.url", url)
        config("--local", "--replace-all", "remote.origin.pushurl", push_url)
        if not source["readOnly"]:
            # Earlier read-only clones also installed this hook. Remove only
            # our unchanged hook when write access is enabled again.
            hook = Path(directory) / ".git" / "hooks" / "pre-push"
            if hook.is_file() and hook.resolve() == hook:
                if hook.read_bytes() == READ_ONLY_HOOK:
                    hook.unlink()
        os.replace(managed / "config.pending", managed / "config")
        # Includes are exact paths, not a global includeIf that could affect
        # unrelated repositories. Preserve all user-owned configuration/hooks.
        includes = subprocess.run(
            ["git", "-C", directory, "config", "--local", "--get-all", "include.path"],
            env=git_environment(),
            capture_output=True,
            text=True,
        ).stdout.splitlines()
        if str(managed / "config") not in includes:
            config("--local", "--add", "include.path", str(managed / "config"))


def repository_url_aliases(url, destination, http_urls):
    aliases = [url]
    http_urls = http_urls or []
    host = destination["host"].rsplit("@", 1)[-1]
    if (
        host == "github.com"
        and destination["port"] == "22"
        or host == "ssh.github.com"
        and destination["port"] == "443"
    ):
        http_urls = [
            *http_urls,
            "https://github.com/" + destination["path"].lstrip("/"),
        ]
    for address in http_urls:
        parsed = urlsplit(address)
        if (
            parsed.scheme not in ("http", "https")
            or not parsed.hostname
            or parsed.username
            or parsed.password
            or parsed.query
            or parsed.fragment
        ):
            raise ValueError("Invalid repository HTTP alias")
        canonical = address.rstrip("/")
        if canonical.endswith(".git"):
            aliases.extend([canonical, canonical[:-4]])
        else:
            aliases.extend([canonical, canonical + ".git"])
    return list(dict.fromkeys(aliases))


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


def checkout_exists(source, timeout=20):
    directory = Path(source["directory"])
    if not directory.is_absolute() or directory.resolve() != directory:
        raise ValueError(f"Repository directory must not contain symlinks: {directory}")
    if not directory.exists():
        return False
    if not directory.is_dir():
        raise ValueError(f"Repository directory is not a directory: {directory}")

    if not (directory / ".git").exists():
        entries = list(directory.iterdir())
        stages = interrupted_clones(source, timeout)
        allowed = {stage.name for stage, _ in stages}
        for _, receipt in stages:
            if receipt and receipt["phase"] == "ready":
                allowed.update(receipt["entries"])
        if any(entry.name not in allowed for entry in entries):
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
                timeout=timeout,
            ).stdout.strip()
        except subprocess.TimeoutExpired:
            raise ValueError(
                f"Timed out inspecting repository {directory} "
                f"(git {' '.join(arguments)} after {timeout:g}s). "
                "Check the workspace filesystem and retry. Existing files were preserved."
            ) from None
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
    expected = source["url"]
    destination = ssh_destination(expected)
    aliases = (
        repository_url_aliases(expected, destination, source.get("httpUrls", []))
        if destination
        else [expected]
    )
    if normalized_repository_url(origin) not in {
        normalized_repository_url(alias) for alias in aliases
    }:
        raise ValueError(
            f"Repository origin does not match the configured source: {directory}. "
            "Existing files were preserved; check the project repository selection."
        )
    return True


def clone_owner():
    try:
        boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
        started = (
            Path(f"/proc/{os.getpid()}/stat").read_text().rsplit(")", 1)[1].split()[19]
        )
        return {"boot": boot, "pid": os.getpid(), "started": started}
    except OSError:
        return None


def clone_is_running(receipt):
    owner = receipt.get("owner") if receipt else None
    if not isinstance(owner, dict):
        return False
    try:
        boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
        process = (
            Path(f"/proc/{int(owner['pid'])}/stat")
            .read_text()
            .rsplit(")", 1)[1]
            .split()
        )
        return (
            owner["boot"] == boot
            and owner["started"] == process[19]
            and process[0] not in ("Z", "X")
        )
    except (OSError, ValueError, KeyError, TypeError, IndexError):
        return False


def interrupted_clones(source, timeout=20):
    directory = Path(source["directory"])
    stages = []
    for stage in sorted(directory.iterdir()):
        if not re.fullmatch(r"\.zentrale-clone-[a-z0-9_]{8}", stage.name):
            continue
        if not stage.is_dir() or stage.resolve() != stage:
            continue
        names = {entry.name for entry in stage.iterdir()}
        if names - {"repository", "clone.json"}:
            continue
        checkout = stage / "repository"
        if checkout.is_symlink() or (
            checkout.exists() and checkout.resolve() != checkout
        ):
            continue
        receipt_file = stage / "clone.json"
        if receipt_file.is_file() and not receipt_file.is_symlink():
            try:
                receipt = json.loads(receipt_file.read_text())
                entries = receipt["entries"]
                valid = (
                    receipt["version"] == 1
                    and receipt["directory"] == str(directory)
                    and receipt["url"] == source["url"]
                    and receipt["phase"] in ("cloning", "ready")
                    and isinstance(entries, list)
                    and all(
                        isinstance(name, str)
                        and name not in ("", ".", "..")
                        and Path(name).name == name
                        for name in entries
                    )
                    and (receipt["phase"] != "ready" or ".git" in entries)
                )
                if valid:
                    metadata = checkout / ".git"
                    if receipt["phase"] == "ready" and metadata.exists():
                        if (
                            metadata.resolve() != metadata
                            or (metadata / "config").is_symlink()
                        ):
                            continue
                        if not checkout_exists(
                            {**source, "directory": str(checkout)}, timeout
                        ):
                            continue
                    stages.append((stage, receipt))
            except (OSError, ValueError, KeyError, TypeError):
                pass
        elif names == {"repository"}:
            # Older versions had no receipt. Recognize only a real checkout of
            # the selected source, and retain every byte when cloning again.
            metadata = checkout / ".git"
            if (
                metadata.is_dir()
                and metadata.resolve() == metadata
                and not (metadata / "config").is_symlink()
            ):
                try:
                    if checkout_exists({**source, "directory": str(checkout)}, timeout):
                        stages.append((stage, None))
                except ValueError:
                    pass
    return stages


def cleanup_clone(stage):
    # Remove only our receipt and empty staging directories, never unknown data.
    checkout = stage / "repository"
    if checkout.is_dir() and not checkout.is_symlink() and not any(checkout.iterdir()):
        checkout.rmdir()
    if {entry.name for entry in stage.iterdir()} == {"clone.json"}:
        (stage / "clone.json").unlink()
        stage.rmdir()


def cleanup_finished_clones(source):
    directory = Path(source["directory"])
    for stage, receipt in interrupted_clones(source):
        if (
            receipt
            and receipt["phase"] == "ready"
            and not clone_is_running(receipt)
            and all(
                (directory / name).exists() or (directory / name).is_symlink()
                for name in receipt["entries"]
            )
        ):
            cleanup_clone(stage)


def publish_clone(source, stage, receipt, previous):
    directory = Path(source["directory"])
    checkout = stage / "repository"
    metadata = checkout / ".git"
    if not metadata.is_dir() or metadata.resolve() != metadata:
        raise ValueError(
            "Interrupted clone has no safe Git metadata; existing files were preserved"
        )
    receipt["owner"] = clone_owner()
    atomic_secret(stage, "clone.json", json.dumps(receipt))
    for abandoned, _ in previous:
        if abandoned == stage:
            continue
        recovery = private_directory(metadata / "zentrale-clone-recovery")
        target = recovery / abandoned.name
        if target.exists() or target.is_symlink():
            raise ValueError(
                "Clone recovery destination already exists; existing files were preserved"
            )
        abandoned.rename(target)
    # A durable receipt makes each rename resumable. Git metadata is published
    # last so inference/editor startup still requires a complete checkout.
    for name in sorted(receipt["entries"], key=lambda name: name == ".git"):
        entry, target = checkout / name, directory / name
        if entry.exists() or entry.is_symlink():
            if target.exists() or target.is_symlink():
                raise ValueError(
                    f"Checkout publication conflicts with {target}; existing files were preserved"
                )
            entry.rename(target)
        elif not (target.exists() or target.is_symlink()):
            raise ValueError(
                f"Interrupted clone is missing {name}; existing files were preserved"
            )
    cleanup_clone(stage)


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
    hook = Path(directory) / ".git" / "hooks" / "pre-push"
    hook.write_bytes(READ_ONLY_HOOK)
    hook.chmod(0o700)


def clone_repository(configuration, environment):
    directory = Path(configuration["directory"])
    previous = interrupted_clones(configuration)
    while any(clone_is_running(receipt) for _, receipt in previous):
        time.sleep(0.2)
        if (directory / ".git").exists() and checkout_exists(configuration):
            cleanup_finished_clones(configuration)
            return
        previous = interrupted_clones(configuration)
    for stage, receipt in previous:
        if receipt and receipt["phase"] == "ready":
            publish_clone(configuration, stage, receipt, previous)
            return

    # Stay on the shared filesystem for atomic entry renames. Completed clones
    # survive interrupted publication; an earlier attempt is retained in .git.
    stage = Path(tempfile.mkdtemp(prefix=".zentrale-clone-", dir=directory))
    checkout = stage / "repository"
    receipt = {
        "version": 1,
        "directory": str(directory),
        "url": configuration["url"],
        "phase": "cloning",
        "entries": [],
        "owner": clone_owner(),
    }
    ready = False
    try:
        atomic_secret(stage, "clone.json", json.dumps(receipt))
        references = []
        for abandoned, _ in previous:
            if (abandoned / "repository" / ".git").is_dir():
                references.extend(
                    ["--reference-if-able", str(abandoned / "repository")]
                )
        subprocess.run(
            [
                "git",
                "-c",
                "core.hooksPath=/dev/null",
                "clone",
                *references,
                "--dissociate",
                "--",
                configuration["url"],
                str(checkout),
            ],
            env=environment,
            check=True,
        )
        if configuration["readOnly"]:
            disable_push(checkout)
        allowed = {stage, *(old for old, _ in previous)}
        if any(entry not in allowed for entry in directory.iterdir()):
            raise ValueError(
                f"Repository directory changed during checkout: {directory}"
            )
        receipt.update(
            phase="ready", entries=[entry.name for entry in checkout.iterdir()]
        )
        atomic_secret(stage, "clone.json", json.dumps(receipt))
        ready = True
        publish_clone(configuration, stage, receipt, previous)
    finally:
        if not ready:
            shutil.rmtree(stage)


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
            + " -o GlobalKnownHostsFile=/dev/null -o "
            + shlex.quote(
                "UserKnownHostsFile=" + json.dumps(known_hosts, ensure_ascii=False)
            )
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
            cleanup_finished_clones(configuration)
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
