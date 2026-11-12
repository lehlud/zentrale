"""Fixed binary upload helper; no file contents are executed or decoded."""
import hashlib
import json
import os
import secrets
import signal
import stat
import sys

LIMIT = 25 * 1024 * 1024


def directory(parent, name):
    try:
        os.mkdir(name, 0o700, dir_fd=parent)
    except FileExistsError:
        pass
    return os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent)


def validate(receipt, name, size):
    if not (receipt and len(receipt) <= 128 and all(c.isascii() and (c.isalnum() or c in "_-") for c in receipt)):
        raise ValueError("invalid attachment ID")
    if not name or name.startswith(".") or "/" in name or "\\" in name or "\x00" in name or len(name) > 205:
        raise ValueError("invalid attachment filename")
    if size < 0 or size > LIMIT:
        raise ValueError("attachment exceeds limit")


def upload(root, receipt, name, size, digest, source, display_name=None):
    validate(receipt, name, size)
    rootfd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    attachmentfd = targetfd = None
    temporary = ".upload-" + secrets.token_hex(16)
    try:
        attachmentfd = directory(rootfd, "attachments")
        targetfd = directory(attachmentfd, receipt)
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=targetfd)
        actual = hashlib.sha256()
        count = 0
        with os.fdopen(fd, "wb") as output:
            while True:
                block = source.read(min(65536, size + 1 - count))
                if not block:
                    break
                count += len(block)
                if count > size:
                    raise ValueError("attachment size mismatch")
                actual.update(block)
                output.write(block)
            if count != size or actual.hexdigest() != digest:
                raise ValueError("incomplete attachment")
            output.flush()
            os.fsync(output.fileno())
        # The ID also commits the name/hash, including the crash window before
        # the database receipt exists. Another filename cannot reuse this ID.
        manifest = json.dumps([display_name or name, size, digest], separators=(",", ":")).encode()
        manifestfd = os.open(temporary + "-manifest", os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=targetfd)
        with os.fdopen(manifestfd, "wb") as output:
            output.write(manifest)
            output.flush()
            os.fsync(output.fileno())
        try:
            os.link(temporary + "-manifest", ".attachment-manifest", src_dir_fd=targetfd, dst_dir_fd=targetfd, follow_symlinks=False)
        except FileExistsError:
            previousfd = os.open(".attachment-manifest", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=targetfd)
            with os.fdopen(previousfd, "rb") as previous:
                if not stat.S_ISREG(os.fstat(previous.fileno()).st_mode) or previous.read(2048) != manifest:
                    raise ValueError("attachment ID conflict")
        try:
            os.link(temporary, name, src_dir_fd=targetfd, dst_dir_fd=targetfd, follow_symlinks=False)
        except FileExistsError:
            existing = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=targetfd)
            with os.fdopen(existing, "rb") as previous:
                info = os.fstat(previous.fileno())
                if not stat.S_ISREG(info.st_mode) or info.st_size != size:
                    raise ValueError("attachment ID conflict")
                checksum = hashlib.sha256()
                while True:
                    block = previous.read(65536)
                    if not block:
                        break
                    checksum.update(block)
                if checksum.hexdigest() != digest:
                    raise ValueError("attachment ID conflict")
        os.fsync(targetfd)
    finally:
        if targetfd is not None:
            for candidate in (temporary, temporary + "-manifest"):
                try:
                    os.unlink(candidate, dir_fd=targetfd)
                except FileNotFoundError:
                    pass
            os.close(targetfd)
        if attachmentfd is not None:
            os.close(attachmentfd)
        os.close(rootfd)


def present(root, receipt, name, size, digest, display_name=None):
    validate(receipt, name, size)
    descriptors = []
    try:
        fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        descriptors.append(fd)
        for component in ("attachments", receipt):
            fd = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            descriptors.append(fd)
        manifestfd = os.open(".attachment-manifest", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)
        with os.fdopen(manifestfd, "rb") as manifest:
            expected = json.dumps([display_name or name, size, digest], separators=(",", ":")).encode()
            if not stat.S_ISREG(os.fstat(manifest.fileno()).st_mode) or manifest.read(2048) != expected:
                raise ValueError("attachment ID conflict")
        sourcefd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)
        with os.fdopen(sourcefd, "rb") as source:
            info = os.fstat(source.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_size != size:
                return False
            checksum = hashlib.sha256()
            while True:
                block = source.read(65536)
                if not block:
                    break
                checksum.update(block)
            return checksum.hexdigest() == digest
    except FileNotFoundError:
        return False
    finally:
        for fd in reversed(descriptors):
            os.close(fd)


def cancelled(signum, frame):
    raise InterruptedError("attachment upload cancelled")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, cancelled)
    signal.signal(signal.SIGHUP, cancelled)
    if len(sys.argv) > 6 and sys.argv[6] == "check":
        print("present" if present(sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5], sys.argv[7] if len(sys.argv) > 7 else None) else "missing")
    else:
        upload(sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5], sys.stdin.buffer, sys.argv[7] if len(sys.argv) > 7 else None)
