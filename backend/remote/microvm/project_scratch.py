"""Storage for the stable host path of a managed project's scratch export."""

import ctypes
import errno
import os
from pathlib import Path
import re
import shutil
import subprocess


def managed_scratch_path(path):
    return path.parent == Path("/tmp") and re.fullmatch(
        r"zentrale-[a-f0-9]{32}-project-[A-Za-z0-9_.-]{1,80}", path.name
    )


def scratch_backing(path):
    return Path("/var/tmp") / ("zentrale-project-scratch-" + str(os.getuid())) / path.name


def open_owned_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    if os.fstat(fd).st_uid != os.getuid():
        os.close(fd)
        raise RuntimeError("project scratch directory is owned by another user: " + str(path))
    return fd


def private_scratch_directory(path, create=False):
    if create:
        path.mkdir(mode=0o700, exist_ok=True)
    fd = open_owned_directory(path)
    try:
        if os.fstat(fd).st_mode & 0o077:
            raise RuntimeError("project scratch backing directory must be private: " + str(path))
    finally:
        os.close(fd)


def resolve_project_scratch(path):
    if not path.is_symlink():
        return path
    backing = scratch_backing(path)
    if not managed_scratch_path(path) or path.readlink() != backing:
        raise RuntimeError("unexpected project scratch symlink: " + str(path))
    private_scratch_directory(backing.parent)
    private_scratch_directory(backing)
    return backing


def prepare_project_scratch(path):
    if not path.exists() and not path.is_symlink():
        backing = scratch_backing(path)
        if managed_scratch_path(path) and backing.exists():
            private_scratch_directory(backing.parent)
            private_scratch_directory(backing)
            try:
                path.symlink_to(backing, target_is_directory=True)
            except FileExistsError:
                pass
        else:
            path.mkdir(mode=0o700, exist_ok=True)
    try:
        fd = open_owned_directory(resolve_project_scratch(path))
    except OSError as error:
        if error.errno not in (errno.ELOOP, errno.ENOTDIR) or not path.is_symlink():
            raise
        # A cold boot can switch the path while another request prepares it.
        fd = open_owned_directory(resolve_project_scratch(path))
    try:
        os.fchmod(fd, 0o700)
    finally:
        os.close(fd)


def reject_scratch_mounts(path):
    with open("/proc/self/mountinfo", encoding="utf-8", errors="surrogateescape") as mounts:
        for line in mounts:
            mount = re.sub(r"\\([0-7]{3})", lambda m: chr(int(m[1], 8)), line.split()[4])
            if Path(mount).is_relative_to(path):
                raise RuntimeError("project scratch contains a mount point: " + str(path))


def scratch_uses_quota_tmpfs():
    with open("/proc/self/mountinfo", encoding="utf-8", errors="surrogateescape") as mounts:
        for line in mounts:
            mount, filesystem = line.strip().split(" - ", 1)
            if mount.split()[4] == "/tmp":
                kind, _, options = filesystem.split()
                return kind == "tmpfs" and bool({"usrquota", "grpquota"} & set(options.split(",")))
    return False


def exchange_paths(first, second):
    # Both names are on the same filesystem. Publish the alias atomically so
    # another workspace preparation never sees a missing scratch directory.
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.renameat2(-100, os.fsencode(first), -100, os.fsencode(second), 2) != 0:
        code = ctypes.get_errno()
        raise OSError(code, os.strerror(code), str(first))


def retire_scratch(path):
    private_scratch_directory(path)
    reject_scratch_mounts(path)
    shutil.rmtree(path)


def offload_project_scratch(path):
    if not managed_scratch_path(path):
        return
    backing = scratch_backing(path)
    retired = path.with_name(path.name + ".offload")
    if path.is_symlink():
        resolve_project_scratch(path)
        if retired.exists():
            retire_scratch(retired)
        return
    if not offload_available(path.parent, backing.parent.parent):
        return

    prepare_project_scratch(path)
    if path.is_symlink():
        return  # A published backing survived loss of /tmp.
    reject_scratch_mounts(path)
    private_scratch_directory(backing.parent, create=True)
    if retired.is_symlink() and retired.readlink() == backing:
        if backing.exists():
            # This copy was never published. Copy the authoritative source
            # again in case it changed after the failed switch.
            retire_scratch(backing)
    else:
        if backing.exists() and any(path.iterdir()):
            raise RuntimeError("both the old and new project scratch directories contain data: " + str(path))
        retired.symlink_to(backing, target_is_directory=True)

    # The caller holds the VM scope lock and has stopped its previous owner.
    # GNU cp preserves hardlinks, modes, sparse files and symlinks in the tree.
    if not backing.exists():
        staging = backing.with_name(backing.name + ".partial")
        if staging.exists() or staging.is_symlink():
            retire_scratch(staging)
        private_scratch_directory(staging, create=True)
        try:
            subprocess.run(
                ["cp", "-a", "--sparse=always", "--", str(path) + "/.", str(staging)],
                check=True, timeout=600,
            )
            staging.rename(backing)
        finally:
            if staging.is_dir():
                retire_scratch(staging)
    private_scratch_directory(backing)
    exchange_paths(path, retired)
    retire_scratch(retired)


def offload_available(temporary_root, disk_root):
    return (
        scratch_uses_quota_tmpfs()
        and disk_root.is_dir()
        and os.access(disk_root, os.W_OK | os.X_OK)
        and temporary_root.stat().st_dev != disk_root.stat().st_dev
    )


def remove_project_scratch(path):
    if not managed_scratch_path(path):
        raise RuntimeError("unsafe project scratch path")
    backing = scratch_backing(path)
    if path.is_symlink():
        if path.readlink() != backing:
            raise RuntimeError("unexpected project scratch symlink: " + str(path))
        path.unlink()
    elif path.exists():
        retire_scratch(path)
    if backing.exists() or backing.is_symlink():
        private_scratch_directory(backing.parent)
        retire_scratch(backing)
    staging = backing.with_name(backing.name + ".partial")
    if staging.exists() or staging.is_symlink():
        private_scratch_directory(backing.parent)
        retire_scratch(staging)
    retired = path.with_name(path.name + ".offload")
    if retired.is_symlink() and retired.readlink() == backing:
        retired.unlink()
    elif retired.exists():
        retire_scratch(retired)
