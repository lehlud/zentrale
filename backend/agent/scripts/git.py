"""Prepare configured checkouts and use deployment keys for Git interactions."""

from contextlib import contextmanager
import fcntl
import hashlib
from itertools import islice
import json
import os
from pathlib import Path
import re
import resource
import shlex
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from urllib.parse import unquote, urlsplit


READ_ONLY_HOOK = b'#!/bin/sh\necho "Read-only source" >&2\nexit 1\n'
CHECKOUT_PROGRESS_FILE = "zentrale-checkout-progress.json"

HOST_KEY_OBSERVER = """from pathlib import Path
import sys

# OpenSSH also calls this command before it has received a host key.
# Record the offered key for the UI and emit no trusted known_hosts records.
if sys.argv[1] == "HOSTNAME":
    Path(__file__).with_name("observed-host-key").write_text(" ".join(sys.argv[2:]), encoding="ascii")
"""


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
        host = parsed.netloc.rsplit("@", 1)[-1]
        if parsed.port:
            host = host.rsplit(":", 1)[0]
        return {
            "host": (unquote(parsed.username) + "@" if parsed.username else "")
            + host.strip("[]"),
            "port": str(parsed.port or 22),
            "path": unquote(parsed.path),
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


def inventory_git_directory(project, storage_root, recorded):
    directory = Path(recorded)
    if directory.is_absolute():
        # Persisted storage coordinates are data, not extra guest mounts.
        for root in (Path(project), Path(storage_root)):
            if directory.is_relative_to(root):
                directory = directory.relative_to(root)
                break
        else:
            raise ValueError("Git credentials are outside project")

    directory = Path(os.path.abspath(Path(project) / directory))
    if not directory.is_relative_to(Path(project)):
        raise ValueError("Git credentials are outside project")

    return str(directory)


def include_git_configuration(directory, configuration):
    result = subprocess.run(
        ["git", "-C", directory, "config", "--local", "--get-all", "include.path"],
        env=git_environment(),
        capture_output=True,
        text=True,
    )
    if result.returncode not in (0, 1):
        result.check_returncode()

    if str(configuration) not in result.stdout.splitlines():
        subprocess.run(
            [
                "git", "-C", directory, "config", "--local", "--add",
                "include.path", str(configuration),
            ],
            env=git_environment(),
            check=True,
            capture_output=True,
        )


def clear_project_git(project, storage_root):
    state = project_git_state(project, create=False)
    if state is None:
        return

    manifest = state / "sources.json"
    if manifest.is_symlink():
        raise ValueError("Git credential manifest must not be a symlink")

    directories = json.loads(manifest.read_text()) if manifest.exists() else []
    for recorded in directories:
        directory = inventory_git_directory(project, storage_root, recorded)
        if not (Path(directory) / ".git").exists():
            continue
        managed = managed_git_directory(project, directory)
        # Keep a fail-closed include rather than falling back to user SSH keys.
        atomic_secret(managed, "config", "[core]\n\tsshCommand = false\n")
        include_git_configuration(directory, managed / "config")
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
    directories = [os.path.relpath(source["directory"], project) for source in sources]
    atomic_secret(state, "sources.json", json.dumps(directories))
    for source in sources:
        if not checkout_exists(source):
            raise ValueError("Cannot configure a missing repository")
        cleanup_finished_clones(source)
        directory = source["directory"]
        managed = managed_git_directory(project, directory)
        url = source["url"]
        destination = ssh_destination(url)

        def config(*arguments):
            try:
                return subprocess.run(
                    ["git", "-C", directory, "config", *arguments],
                    env=git_environment(), check=True, capture_output=True, text=True,
                )
            except subprocess.CalledProcessError as error:
                if error.stderr:
                    raise ValueError(error.stderr.strip()) from None
                raise

        # Git locks the file being built. A killed helper must not leave a lock
        # at a filename that any subsequent preparation will also use.
        with tempfile.TemporaryDirectory(prefix="config-", dir=managed) as staging:
            pending = Path(staging) / "config"
            pending.touch(mode=0o600)

            def setting(name, value):
                config("--file", str(pending), name, value)

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
                    config("--file", str(pending), "--add", "url." + url + ".insteadOf", alias)

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
            os.replace(pending, managed / "config")
        # Includes are exact paths, not a global includeIf that could affect
        # unrelated repositories. Preserve all user-owned configuration/hooks.
        include_git_configuration(directory, managed / "config")


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


ARCHIVE_DIRECTORY = ".zentrale-previous-checkouts"


def archive_checkout(directory, stage, metadata, entries):
    archive = private_directory(metadata / ARCHIVE_DIRECTORY)
    backup = private_directory(archive / stage.name.removeprefix(".zentrale-clone-"))
    for entry in entries:
        source, target = directory / entry, backup / entry
        if os.path.lexists(source):
            if os.path.lexists(target):
                raise ValueError(f"Repository archive conflicts with {target}")
            source.rename(target)
        elif not os.path.lexists(target):
            raise ValueError(f"Repository archive is missing {entry}")


def checkout_exists(source, timeout=20, apply_move=False):
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
        allowed = {*(source.get("attachedNames") or []), *(stage.name for stage, _ in stages)}
        for _, receipt in stages:
            if receipt and receipt["phase"] == "ready":
                allowed.update(receipt["entries"])
                allowed.update(receipt.get("archiveEntries", []))
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
        if apply_move and normalized_repository_url(origin) in {
            normalized_repository_url(alias) for alias in (source.get("movedFrom") or [])
        }:
            metadata = directory / ".git"
            config = metadata / "config"
            if (
                not metadata.is_dir() or metadata.resolve() != metadata
                or not config.is_file() or config.is_symlink()
                or read_git("config", "--file", str(config), "--get-all", "remote.origin.url") != origin
            ):
                raise ValueError("Repository origin is not a single local setting")
            subprocess.run(
                ["git", "-C", str(directory), "config", "--file", str(config), "--replace-all", "remote.origin.url", expected],
                env=git_environment(), check=True, capture_output=True, timeout=timeout,
            )
            return True
        if normalized_repository_url(origin) in {
            normalized_repository_url(alias) for alias in (source.get("replaceFrom") or [])
        }:
            metadata = directory / ".git"
            if not metadata.is_dir() or metadata.resolve() != metadata:
                raise ValueError("Repository replacement requires a self-contained checkout")
            return False  # The selected repository gets a separate checkout.
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


def valid_entry_names(entries):
    return isinstance(entries, list) and all(
        isinstance(name, str) and name not in ("", ".", "..") and Path(name).name == name
        for name in entries
    )


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
                archive_entries = receipt.get("archiveEntries")
                valid = (
                    receipt["version"] == 1
                    and receipt["directory"] == str(directory)
                    and receipt["url"] == source["url"]
                    and receipt["phase"] in ("cloning", "fetched", "ready")
                    and valid_entry_names(entries)
                    and (
                        "archiveEntries" not in receipt
                        or (
                            source.get("replaceFrom")
                            and receipt["phase"] == "ready"
                            and valid_entry_names(archive_entries)
                            and ".git" in archive_entries
                            and stage.name not in archive_entries
                            and (checkout / ".git").is_dir()
                        )
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
    replacing = (directory / ".git").exists() and not checkout_exists(source)
    attached = set(source.get("attachedNames") or [])
    if attached.intersection(receipt["entries"]):
        raise ValueError("The new repository contains a directory reserved for an attached source")
    receipt["owner"] = clone_owner()
    if replacing and "archiveEntries" not in receipt:
        preserved = attached | {stage.name, *(old.name for old, _ in previous)}
        receipt["archiveEntries"] = [
            entry.name for entry in directory.iterdir() if entry.name not in preserved
        ]
    # Use the completed clone's receipt to resume the archive without accepting
    # instructions from files in either repository's worktree.
    atomic_secret(stage, "clone.json", json.dumps(receipt))
    (metadata / CHECKOUT_PROGRESS_FILE).unlink(missing_ok=True)
    if "archiveEntries" in receipt:
        archive_checkout(directory, stage, metadata, receipt.pop("archiveEntries"))
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


CLONE_PROGRESS = re.compile(
    r"^(?:remote: )?(Enumerating objects|Counting objects|Compressing objects|"
    r"Receiving objects|Resolving deltas|Updating files|Checking out files):\s+"
    r"(?:(\d{1,3})%\s*)?\(?(\d+(?:/\d+)?)\)?"
    r"(?:, (\d+(?:\.\d+)? (?:bytes?|[KMGT]iB))"
    r"(?: \| (\d+(?:\.\d+)? (?:bytes?|[KMGT]iB)/s))?)?"
)
CHECKOUT_ACTIVITY_INTERVAL = 5
FIRST_CHECKOUT_BATCH = 4096
CHECKOUT_BATCH = 16384


def report_clone_progress(step, percent="-1", count="", transfer=""):
    print(
        f"ZENTRALE_CLONE_PROGRESS {step} {percent}\t{count}\t{transfer}",
        file=sys.stderr, flush=True,
    )


def written_by_process(pid):
    try:
        with open(f"/proc/{pid}/io", encoding="ascii") as counters:
            for line in counters:
                if line.startswith("wchar:"):
                    return int(line.split()[1])
    except (OSError, ValueError):
        pass
    return None


def checkout_activity(pid):
    process = Path("/proc") / str(pid)
    try:
        stat = (process / "stat").read_text().rsplit(")", 1)[1].split()
        ticks = int(stat[11]) + int(stat[12])
    except (OSError, ValueError, IndexError):
        return None
    try:
        children = (process / "task" / str(pid) / "children").read_text().split()
    except OSError:
        children = []
    writes = {}
    for checkout_pid in (str(pid), *children):
        counter = written_by_process(checkout_pid)
        if counter is not None:
            writes[checkout_pid] = counter
    return ticks, writes


def run_clone(arguments, environment, report_progress, watch_checkout=False, stdin=None):
    if not report_progress:
        subprocess.run(arguments, env=environment, check=True, stdin=stdin)
        return

    with subprocess.Popen(
        arguments, env={**environment, "LC_ALL": "C"}, stdin=stdin, stderr=subprocess.PIPE,
        text=True, encoding="utf-8", errors="replace",
    ) as process:
        previous = None
        last_update = 0
        output_lock = threading.Lock()
        stopped = threading.Event()

        def sample_checkout():
            activity = checkout_activity(process.pid)
            written = 0
            while activity is not None and not stopped.wait(CHECKOUT_ACTIVITY_INTERVAL):
                last_ticks, last_writes = activity
                activity = checkout_activity(process.pid)
                if activity is None:
                    return
                ticks, writes = activity
                # Workers can finish between samples. Their previous output
                # still counts, even after their process counters disappear.
                delta = sum(max(0, value - last_writes.get(pid, 0)) for pid, value in writes.items())
                written += delta
                with output_lock:
                    quiet = time.monotonic() - last_update >= CHECKOUT_ACTIVITY_INTERVAL
                    if delta and quiet and stdin is None:
                        report_clone_progress(
                            "Writing checkout data", count=str(written),
                            transfer=f"{written / (1024 * 1024):.2f} MiB",
                        )
                    elif quiet and (delta or ticks > last_ticks):
                        # Keep the idle timer alive during path queuing or a
                        # batch in progress without advancing the file count.
                        print("ZENTRALE_CLONE_ACTIVE", file=sys.stderr, flush=True)

        watcher = None
        if watch_checkout:
            watcher = threading.Thread(target=sample_checkout)
            watcher.start()
        try:
            for line in process.stderr:
                with output_lock:
                    progress = CLONE_PROGRESS.match(line)
                    if progress is None:
                        sys.stderr.write(line)
                        continue
                    step, percent, count, received, rate = progress.groups()
                    position = (step, percent, count, received)
                    now = time.monotonic()
                    milestone = previous is None or step != previous[0] or (
                        percent == "100" and percent != previous[1]
                    )
                    if position != previous and (milestone or now - last_update >= 2):
                        transfer = " | ".join(value for value in (received, rate) if value)
                        report_clone_progress(step, percent or "-1", count, transfer)
                        previous, last_update = position, now
            status = process.wait()
        finally:
            stopped.set()
            if watcher:
                watcher.join()
        if status:
            raise subprocess.CalledProcessError(status, arguments)


def usable_clone_metadata(checkout):
    metadata = checkout / ".git"
    return (
        metadata.is_dir()
        and metadata.resolve() == metadata
        and (metadata / "config").is_file()
        and not (metadata / "config").is_symlink()
        and not os.path.lexists(metadata / "objects/info/alternates")
    )


def interrupted_legacy_checkout(source, stage, receipt, environment):
    # The old git clone completed and dissociated its download before taking
    # the index lock for checkout. Leave its partial worktree in recovery.
    checkout = stage / "repository"
    lock = checkout / ".git/index.lock"
    return (
        receipt["phase"] == "cloning"
        and usable_clone_metadata(checkout)
        and lock.is_file()
        and not lock.is_symlink()
        and not os.path.lexists(checkout / ".git/index")
        and checkout_exists({**source, "directory": str(checkout)})
        and subprocess.run(
            ["git", "-C", str(checkout), "rev-parse", "--verify", "HEAD^{tree}"],
            env=environment, capture_output=True, timeout=20,
        ).returncode == 0
    )


def nul_paths(manifest):
    pending = b""
    while block := manifest.read(1024 * 1024):
        *paths, pending = (pending + block).split(b"\0")
        yield from paths
    if pending:
        raise ValueError("Git returned an incomplete checkout path")


def checkout_scratch_directory(directory):
    key = hashlib.sha256(os.fsencode(directory)).hexdigest()
    return private_directory(Path("/var/tmp") / ("zentrale-checkout-" + key))


@contextmanager
def checkout_scratch(checkout, environment):
    if environment.get("ZENTRALE_SANDBOX") != "microvm":
        with tempfile.TemporaryDirectory(prefix="zentrale-checkout-") as directory:
            yield Path(directory)
        return
    # Retain at most one scratch index per destination across interrupted runs.
    directory = checkout_scratch_directory(checkout.parent.parent)
    with (directory / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        (directory / "index.lock").unlink(missing_ok=True)
        yield directory
        (directory / "index").unlink(missing_ok=True)
        (directory / "identity").unlink(missing_ok=True)


def completed_checkout_paths(progress, identity, total):
    if progress.is_symlink():
        return 0
    try:
        data = json.loads(progress.read_text())
        done = data["completed"]
        if type(done) is int and 0 <= done <= total and data["identity"] == identity:
            return done
    except (OSError, ValueError, TypeError, KeyError):
        pass
    return 0


def checkout_in_batches(git, checkout, head, environment, report_progress):
    # The repository lives on 9p. Repeated index reads and rewrites use the
    # guest's own disk until the complete index is ready for publication.
    identity = hashlib.sha256(os.fsencode(checkout) + b"\0" + head).hexdigest()
    progress = checkout / ".git" / CHECKOUT_PROGRESS_FILE
    with checkout_scratch(checkout, environment) as scratch:
        with tempfile.TemporaryFile(dir=scratch) as manifest:
            subprocess.run(
                [*git, "ls-tree", "-r", "--name-only", "-z", "HEAD"],
                env=environment, stdout=manifest, check=True,
            )
            manifest.seek(0)
            total = sum(block.count(b"\0") for block in iter(lambda: manifest.read(1024 * 1024), b""))
            if total <= FIRST_CHECKOUT_BATCH:
                return False
            complete = completed_checkout_paths(progress, identity, total)
            if report_progress:
                if complete:
                    report_clone_progress("Updating files", str(100 * complete // total), f"{complete}/{total}")
                else:
                    report_clone_progress("Checking out files", "0")
            index = scratch / "index"
            indexed = {**environment, "GIT_INDEX_FILE": str(index)}
            marker = scratch / "identity"
            if (not index.is_file() or index.is_symlink() or not marker.is_file()
                    or marker.is_symlink() or marker.read_bytes() != identity.encode()):
                index.unlink(missing_ok=True)
                run_clone(
                    [*git, "-c", "index.version=4", "read-tree", "HEAD"], indexed,
                    report_progress, watch_checkout=True, stdin=subprocess.DEVNULL,
                )
                atomic_secret(scratch, marker.name, identity)
            manifest.seek(0)
            paths = nul_paths(manifest)
            batch_size = CHECKOUT_BATCH if complete else FIRST_CHECKOUT_BATCH
            if complete:
                for _ in islice(paths, complete):
                    pass
            while batch := list(islice(paths, batch_size)):
                with tempfile.TemporaryFile(dir=scratch) as items:
                    items.write(b"\0".join(batch) + b"\0")
                    items.seek(0)
                    run_clone(
                        [*git, "checkout-index", "--index", "--force", "-z", "--stdin"],
                        indexed, report_progress, watch_checkout=True, stdin=items,
                    )
                complete += len(batch)
                atomic_secret(progress.parent, progress.name, json.dumps({
                    "identity": identity, "completed": complete,
                }))
                if report_progress and complete < total:
                    report_clone_progress("Updating files", str(100 * complete // total), f"{complete}/{total}")
                batch_size = CHECKOUT_BATCH
        lock = checkout / ".git" / "index.lock"
        target = lock.open("xb")
        try:
            with target, index.open("rb") as source:
                shutil.copyfileobj(source, target, 1024 * 1024)
            lock.replace(lock.with_name("index"))
        except BaseException:
            lock.unlink(missing_ok=True)
            raise
        if report_progress:
            report_clone_progress("Updating files", "100", f"{complete}/{total}")
        return True


def checkout_files(checkout, receipt, configuration, environment, report_progress):
    metadata = checkout / ".git"
    (metadata / "index.lock").unlink(missing_ok=True)
    git = ["git", "-C", str(checkout), "-c", "core.hooksPath=/dev/null"]
    paths = receipt.get("checkoutPaths", [])
    if receipt.get("resetIndex"):
        # The index was created for a different worktree and path selection.
        # Only private staging is replaced. Its previous files are archived.
        (metadata / "index").unlink(missing_ok=True)
        if not paths:
            subprocess.run(
                [*git, "config", "--worktree", "core.sparseCheckout", "false"],
                env=environment, check=True,
            )
        del receipt["resetIndex"]
        atomic_secret(checkout.parent, "clone.json", json.dumps(receipt))
    head = subprocess.run(
        [*git, "rev-parse", "--verify", "--quiet", "HEAD"],
        env=environment, capture_output=True, timeout=20,
    )
    if head.returncode == 1 and not paths:
        return  # Empty upstream repository.
    head.check_returncode()
    if configuration.get("managedVM"):
        # Read attributes from Git instead of consulting the 9p worktree.
        environment = {**environment, "GIT_ATTR_SOURCE": "HEAD"}
    if paths:
        if report_progress:
            report_clone_progress("Checking out selected directories")
        for directory in paths:
            kind = subprocess.run(
                [*git, "cat-file", "-t", "HEAD:" + directory],
                env=environment, capture_output=True, timeout=20,
            )
            if kind.returncode or kind.stdout.strip() != b"tree":
                raise ValueError(f"Checkout directory is not in HEAD: {directory}")
        subprocess.run(
            [*git, "sparse-checkout", "set", "--cone", "--sparse-index", "--", *paths],
            env=environment, check=True,
        )
    if configuration.get("managedVM"):
        # Git uses one worker per online CPU with 0. On very large guests,
        # leave process slots for the launcher, progress monitor and Git itself.
        processes, _ = resource.getrlimit(resource.RLIMIT_NPROC)
        cpus = os.cpu_count() or 1
        workers = 0
        if processes != resource.RLIM_INFINITY and processes < cpus + 4:
            workers = max(1, processes - 4)
        git.extend(["-c", f"checkout.workers={workers}"])
        if not paths and checkout_in_batches(git, checkout, head.stdout.strip(), environment, report_progress):
            return
    if not paths and report_progress:
        report_clone_progress("Checking out files", "0")
    run_clone(
        [*git, "checkout", "--force", *(["--progress"] if report_progress else [])],
        environment, report_progress, watch_checkout=True,
    )


def clone_repository(configuration, environment):
    if environment.get("ZENTRALE_SANDBOX") != "microvm":
        return prepare_clone(configuration, environment)
    scratch = checkout_scratch_directory(configuration["directory"])
    # The remote daemon may still be draining a canceled invocation. Serialize
    # preparation and publication across PID namespaces in the same guest.
    with (scratch / "checkout.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            if configuration.get("reportProgress"):
                report_clone_progress("Waiting for checkout")
            fcntl.flock(lock, fcntl.LOCK_EX)
        return prepare_clone(configuration, environment)


def prepare_clone(configuration, environment):
    directory = Path(configuration["directory"])
    report_progress = configuration.get("reportProgress", False)
    paths = sorted(set(configuration.get("checkoutPaths") or []))
    previous = interrupted_clones(configuration)
    if report_progress and any(clone_is_running(receipt) for _, receipt in previous):
        report_clone_progress("Waiting for checkout")
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

    reusable = next(
        ((stage, receipt) for stage, receipt in previous
         if receipt["phase"] == "fetched" and usable_clone_metadata(stage / "repository")),
        None,
    )
    fetched = reusable if reusable and reusable[1].get("checkoutPaths", []) == paths else None
    if fetched is None:
        # Staging on the shared filesystem allows metadata and completed
        # worktree entries to be published with atomic renames.
        stage = Path(tempfile.mkdtemp(prefix=".zentrale-clone-", dir=directory))
        receipt = {
            "version": 1,
            "directory": str(directory),
            "url": configuration["url"],
            "phase": "cloning",
            "entries": [],
            "checkoutPaths": paths,
        }
    else:
        stage, receipt = fetched
    checkout = stage / "repository"
    receipt["owner"] = clone_owner()
    try:
        atomic_secret(stage, "clone.json", json.dumps(receipt))
        if fetched is None:
            recovery = reusable[0] if reusable else next((
                old for old, old_receipt in previous
                if interrupted_legacy_checkout(configuration, old, old_receipt, environment)
            ), None)
            if recovery:
                checkout.mkdir()
                # Record the destination first. On interruption before the
                # rename, the old receipt still points at the only metadata.
                receipt["resetIndex"] = bool(reusable)
                atomic_secret(stage, "clone.json", json.dumps({**receipt, "phase": "fetched"}))
                (recovery / "repository/.git").rename(checkout / ".git")
                receipt["phase"] = "fetched"
            else:
                references = []
                for abandoned, _ in previous:
                    if (abandoned / "repository" / ".git").is_dir():
                        references.extend(["--reference-if-able", str(abandoned / "repository")])
                if report_progress:
                    report_clone_progress("Connecting to repository")
                run_clone(
                    ["git", "-c", "core.hooksPath=/dev/null", "clone", "--no-checkout",
                     *(["--progress"] if report_progress else []), *references,
                     "--dissociate", "--", configuration["url"], str(checkout)],
                    environment, report_progress,
                )
                receipt["phase"] = "fetched"
                atomic_secret(stage, "clone.json", json.dumps(receipt))

        checkout_files(checkout, receipt, configuration, environment, report_progress)

        if configuration["readOnly"]:
            disable_push(checkout)
        allowed = {stage, *(old for old, _ in previous),
                   *(directory / name for name in (configuration.get("attachedNames") or []))}
        if any(entry not in allowed for entry in directory.iterdir()) and (
            not (directory / ".git").exists() or checkout_exists(configuration)
        ):
            raise ValueError(
                f"Repository directory changed during checkout: {directory}"
            )
        receipt.update(
            phase="ready", entries=[entry.name for entry in checkout.iterdir()]
        )
        atomic_secret(stage, "clone.json", json.dumps(receipt))
        publish_clone(configuration, stage, receipt, previous)
    finally:
        if receipt["phase"] == "cloning":
            shutil.rmtree(stage)


def run_git(configuration):
    temporary_directory = tempfile.mkdtemp(prefix="git-")
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
        observe_key = configuration["operation"] == "prepare" and configuration.get(
            "repositoryHostKey", {}
        ).get("repositorySourceId")
        observed = Path(temporary_directory) / "observed-host-key"
        if observe_key:
            observer = write_secret(temporary_directory, "observe.py", HOST_KEY_OBSERVER)
            command = shlex.join([
                sys.executable.replace("%", "%%"), "-I", observer.replace("%", "%%"),
                "%I", "%t", "%K",
            ])
            environment["GIT_SSH_COMMAND"] += (
                " -o IgnoreUnknown=KnownHostsCommand -o "
                + shlex.quote("KnownHostsCommand=" + command)
            )
        try:
            if configuration["operation"] in ("clone", "prepare"):
                clone_repository(configuration, environment)
            else:
                arguments = ["git", "-C", directory, *configuration["arguments"]]
                subprocess.run(arguments, env=environment, check=True)
        except subprocess.CalledProcessError:
            if observe_key and observed.is_file():
                offered = observed.read_text(encoding="ascii").split()
                trusted = [line.split()[1:3] for line in configuration["known"].splitlines()]
                if len(offered) == 2 and offered not in trusted:
                    print("ZENTRALE_REPOSITORY_HOST_KEY " + " ".join(offered), file=sys.stderr)
                    sys.exit(79)
            raise
        print(
            json.dumps({"directory": directory, "readOnly": configuration["readOnly"]})
        )
    finally:
        shutil.rmtree(temporary_directory)


def main():
    configuration = json.load(sys.stdin)
    # Resolve structured paths against the process CWD, never a host mount alias.
    for checkout in [configuration, *configuration.get("sources", [])]:
        for field in ("project", "directory"):
            if field in checkout:
                checkout[field] = os.path.abspath(checkout[field])

    if configuration["operation"] == "clear-project-git":
        clear_project_git(
            configuration["project"],
            configuration.get("storageRoot", configuration["project"]),
        )
        return
    if configuration["operation"] == "configure-project-git":
        configure_project_git(configuration)
        return
    if configuration["operation"] == "inspect":
        missing = []
        for source in configuration["sources"]:
            if not checkout_exists(source, apply_move=True):
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
