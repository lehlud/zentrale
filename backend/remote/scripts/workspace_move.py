"""Copy and retire project-owned directories without following symlinks."""

import fcntl
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

operation, name, directory, token, move = sys.argv[1:]
if not re.fullmatch(r"project-[A-Za-z0-9_-]+", name) or not re.fullmatch(r"[A-Za-z0-9_-]+", move):
    sys.exit("Invalid workspace move identity")

directory = Path(directory)
metadata = directory.parent / ".zentrale-workspaces"
marker = metadata / (name + ".json")
alias = metadata / (name + "-move-source-" + move)
receipt = metadata / (name + "-move-destination-" + move)
stage = metadata / (name + "-move-stage-" + move)
ready = metadata / (name + "-move-ready-" + move)
trash = metadata / (name + "-move-retired-" + move)
lock = metadata / (name + "-move-lock-" + move)


def save(path, value):
    fd, tmp = tempfile.mkstemp(dir=metadata)
    try:
        with os.fdopen(fd, "w") as file:
            json.dump(value, file)
            file.flush()
            os.fsync(file.fileno())
        os.replace(tmp, path)
    finally:
        Path(tmp).unlink(missing_ok=True)


def inode(path):
    return path.stat().st_ino


def reject_mounts():
    def unescape(value):
        return re.sub(r"\\([0-7]{3})", lambda m: chr(int(m[1], 8)), value)

    with open("/proc/self/mountinfo", encoding="utf-8", errors="surrogateescape") as mounts:
        for line in mounts:
            mount = Path(unescape(line.split()[4]))
            if mount.is_relative_to(directory):
                raise ValueError("The workspace contains a mount point")


def main():
    if operation == "remove-scratch":
        if directory.parent != Path("/tmp") or directory.resolve() != directory or not re.fullmatch(
            r"zentrale-[a-f0-9]{32}-" + name, directory.name
        ):
            raise ValueError("Unsafe project scratch path")
        if directory.exists():
            if directory.stat().st_uid != os.getuid():
                raise ValueError("Project scratch directory is owned by another user")
            reject_mounts()
            shutil.rmtree(directory)
        return
    if directory.name != name or not directory.is_absolute() or directory.resolve() != directory:
        raise ValueError("Unsafe managed workspace directory")
    if any(p.is_symlink() for p in (metadata, marker, alias, receipt, stage, trash, ready, lock)):
        raise ValueError("Unsafe workspace move metadata")
    if operation == "remove" and not directory.exists() and not marker.exists() and not trash.exists():
        if alias.exists() and json.loads(alias.read_text()) == token:
            alias.unlink()
        return
    if json.loads(marker.read_text()).get("token") != token:
        raise ValueError("Workspace ownership has changed")
    if operation == "remove":
        reject_mounts()
        if any(metadata.glob(name + "-move-destination-" + move.rsplit("-", 1)[0] + "-*")):
            raise ValueError("The old workspace is also a destination for this move")
        if json.loads(alias.read_text()) != token:
            raise ValueError("Missing source receipt for workspace cleanup")
        if directory.exists():
            if not directory.is_dir() or trash.exists():
                raise ValueError("Workspace has changed during cleanup")
            directory.rename(trash)
        if trash.exists():
            shutil.rmtree(trash)
        marker.unlink()
        alias.unlink(missing_ok=True)
        return
    if operation == "finish":
        if not directory.is_dir():
            raise ValueError("Destination workspace is missing")
        ready.unlink(missing_ok=True)
        lock.unlink(missing_ok=True)
        for incomplete in metadata.glob(stage.name + "-incoming-*"):
            if re.fullmatch(re.escape(stage.name) + r"-incoming-[a-z0-9_]{8}", incomplete.name):
                shutil.rmtree(incomplete, ignore_errors=True)
        alias.unlink(missing_ok=True)
        receipt.unlink(missing_ok=True)
        return
    if not directory.is_dir():
        raise ValueError("Managed workspace is missing")
    if operation == "mark":
        save(alias, token)
    elif operation == "check":
        if alias.exists() and json.loads(alias.read_text()) == token:
            print("shared")
        elif receipt.exists():
            verified = json.loads(receipt.read_text())
            if verified == inode(directory) and not stage.exists():
                print("published")
            elif any(directory.iterdir()):
                raise ValueError("The destination has changed since it was verified")
            else:
                print("empty")
        elif any(directory.iterdir()):
            raise ValueError("The destination already contains a checkout or other files")
        else:
            print("empty")
    elif operation.startswith("snapshot-"):
        backups = directory / ".zentrale-vm-backups"
        owner = backups / ".owner"
        destination = backups / (move + ".qcow2")
        pending = backups / (move + ".pending")
        completed = metadata / (name + "-snapshot-ready-" + move)
        if any(p.is_symlink() for p in (backups, owner, destination, pending, completed)):
            raise ValueError("Unsafe VM backup path")
        if not backups.exists():
            backups.mkdir(mode=0o700)
        if not owner.exists():
            if any(backups.iterdir()):
                raise ValueError("A directory is already using the VM backup path")
            save(owner, "zentrale-vm-backups-v1")
        if json.loads(owner.read_text()) != "zentrale-vm-backups-v1":
            raise ValueError("The VM backup directory does not belong to this project")
        if operation == "snapshot-check":
            print("published" if destination.is_file() else "empty")
        elif operation == "snapshot-receive":
            completed.unlink(missing_ok=True)
            with open(pending, "wb") as file:
                shutil.copyfileobj(sys.stdin.buffer, file, 1024 * 1024)
                file.flush()
                os.fsync(file.fileno())
            save(completed, pending.stat().st_size)
        elif operation == "snapshot-publish":
            if not pending.is_file() or not completed.is_file() or pending.stat().st_size != json.loads(completed.read_text()) or pending.stat().st_size == 0:
                raise ValueError("VM snapshot transfer is incomplete")
            os.chmod(pending, 0o600)
            os.replace(pending, destination)
            completed.unlink(missing_ok=True)
            subprocess.run(["sync", "-f", str(backups)], check=True)
        else:
            raise ValueError("Invalid snapshot operation")
    elif operation == "pack":
        reject_mounts()
        # GNU tar preserves hardlinks, modes, timestamps, symlinks and sparse files.
        subprocess.run(["tar", "--sparse", "--one-file-system", "-C", str(directory), "-cf", "-", "."], check=True)
    elif operation == "extract":
        if any(directory.iterdir()):
            raise ValueError("The destination already contains project files")
        # A process left behind by a disconnected controller must never write
        # into the next attempt's staging directory.
        with tempfile.TemporaryDirectory(prefix=stage.name + "-incoming-", dir=metadata) as temporary:
            incoming = Path(temporary) / "workspace"
            incoming.mkdir(mode=0o700)
            subprocess.run(["tar", "--no-same-owner", "--same-permissions", "-C", str(incoming), "-xf", "-"], check=True)
            while sys.stdin.buffer.read(65536):
                pass  # Consume final archive padding and wait for the sender.
            subprocess.run(["sync", "-f", str(incoming)], check=True)
            with lock.open("a") as guard:
                fcntl.flock(guard, fcntl.LOCK_EX)
                if receipt.exists() and json.loads(receipt.read_text()) == inode(directory):
                    return
                if any(directory.iterdir()):
                    raise ValueError("The destination changed during the transfer")
                ready.unlink(missing_ok=True)
                if stage.exists():
                    shutil.rmtree(stage)
                incoming.rename(stage)
                save(ready, inode(stage))
    elif operation == "publish":
        with lock.open("a") as guard:
            fcntl.flock(guard, fcntl.LOCK_EX)
            if receipt.exists() and json.loads(receipt.read_text()) == inode(directory):
                return
            if any(directory.iterdir()) or not stage.is_dir() or not ready.is_file() or json.loads(ready.read_text()) != inode(stage):
                raise ValueError("Staged copy is missing or the destination has changed")
            save(receipt, inode(stage))
            # A replacement rename of an empty directory is atomic. Publish
            # only after both hosts confirmed the transfer.
            os.replace(stage, directory)
            subprocess.run(["sync", "-f", str(directory)], check=True)
    else:
        raise ValueError("Invalid workspace move operation")


try:
    main()
except (OSError, ValueError, subprocess.CalledProcessError) as error:
    sys.exit("Workspace move: " + str(error))
