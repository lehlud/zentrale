"""Receive framed dataset bytes; never receive credentials or execute datasets."""

import errno
import hashlib
import json
import os
import re
import secrets
import signal
import struct
import sys
import unicodedata


class DownloadError(Exception):
    pass


def read_exact(source, count):
    chunks = bytearray()

    while len(chunks) < count:
        chunk = source.read(count - len(chunks))

        if not chunk:
            raise DownloadError("Transfer incomplete; destination was not committed")

        chunks.extend(chunk)

    return bytes(chunks)


def read_json_line(source):
    line = source.readline(1025)

    if not line.endswith(b"\n") or len(line) > 1024:
        raise DownloadError("Invalid transfer metadata")

    try:
        value = json.loads(line)
    except (UnicodeError, ValueError) as error:
        raise DownloadError("Invalid transfer metadata") from error

    if not isinstance(value, dict):
        raise DownloadError("Invalid transfer metadata")

    return value


def destination_parent(destination):
    if not isinstance(destination, str) or len(destination.encode()) > 1024:
        raise DownloadError("A project-relative destination is required")

    parts = destination.split("/")
    invalid = any(part in ("", ".", "..") for part in parts)
    controls = any(unicodedata.category(character) == "Cc" for character in destination)

    if invalid or controls or "\\" in destination:
        raise DownloadError("A literal project-relative destination is required")

    descriptor = os.open(".", os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)

    try:
        for part in parts[:-1]:
            try:
                os.mkdir(part, mode=0o700, dir_fd=descriptor)
            except FileExistsError:
                pass

            child = os.open(
                part,
                os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                dir_fd=descriptor,
            )
            os.close(descriptor)
            descriptor = child

        try:
            os.stat(parts[-1], dir_fd=descriptor, follow_symlinks=False)
        except FileNotFoundError:
            return descriptor, parts[-1]

        raise DownloadError("Destination already exists; existing files are never overwritten")
    except BaseException:
        os.close(descriptor)
        raise


def receive(options, source):
    maximum = options.get("maxBytes")
    expected_digest = options.get("sha256", "")

    if type(maximum) is not int or maximum < 1:
        raise DownloadError("A positive maxBytes limit is required")

    if not isinstance(expected_digest, str) or (
        expected_digest and re.fullmatch(r"[a-fA-F0-9]{64}", expected_digest) is None
    ):
        raise DownloadError("Invalid expected SHA-256 digest")

    expected_digest = expected_digest.lower()

    if read_exact(source, 5) != b"ZHF1\n":
        raise DownloadError("Invalid transfer header")

    expected_size = read_json_line(source).get("size")

    if type(expected_size) is not int or expected_size < -1 or expected_size > maximum:
        raise DownloadError("Invalid or oversized dataset file")

    parent, filename = destination_parent(options.get("destination"))
    staging = ".zentrale-hf-" + secrets.token_hex(16)
    created = False

    try:
        capacity = os.fstatvfs(parent)

        if expected_size >= 0 and expected_size > capacity.f_bavail * capacity.f_frsize:
            raise DownloadError("Insufficient free space for this dataset file")

        descriptor = os.open(
            staging,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
            0o600,
            dir_fd=parent,
        )
        created = True
        digest = hashlib.sha256()
        total = 0

        with os.fdopen(descriptor, "wb") as output:
            while True:
                length = struct.unpack(">I", read_exact(source, 4))[0]

                if length == 0:
                    break

                if length > 1024 * 1024 or length > maximum - total:
                    raise DownloadError("Dataset transfer exceeds its approved limit")

                chunk = read_exact(source, length)
                output.write(chunk)
                digest.update(chunk)
                total += length

            trailer = read_json_line(source)
            actual_digest = digest.hexdigest()
            trailer_size = trailer.get("size")
            complete = (
                type(trailer_size) is int
                and trailer_size == total
                and trailer.get("sha256") == actual_digest
            )

            if not complete or source.read(1) or (expected_size >= 0 and expected_size != total):
                raise DownloadError("Transfer incomplete or corrupted; destination was not committed")

            if expected_digest and expected_digest != actual_digest:
                raise DownloadError("SHA-256 mismatch; destination was not committed")

            output.flush()
            os.fsync(output.fileno())

        os.link(staging, filename, src_dir_fd=parent, dst_dir_fd=parent, follow_symlinks=False)
        os.fsync(parent)

        return {"path": options["destination"], "bytes": total, "sha256": actual_digest}
    finally:
        try:
            if created:
                os.unlink(staging, dir_fd=parent)
        finally:
            os.close(parent)


def cancel_transfer(_signal, _frame):
    raise DownloadError("Transfer cancelled; no incomplete destination was committed")


def main():
    signal.signal(signal.SIGTERM, cancel_transfer)

    try:
        options = json.loads(sys.argv[1])
        result = receive(options, sys.stdin.buffer)
        print(json.dumps(result, ensure_ascii=False))
    except DownloadError as error:
        print(str(error), file=sys.stderr)
        return 1
    except OSError as error:
        if error.errno == errno.EEXIST:
            message = "Destination already exists; existing files are never overwritten"
        elif error.errno in (errno.ENOSPC, errno.EDQUOT):
            message = "Insufficient space or storage quota for this dataset file"
        else:
            message = "Cannot write dataset file safely; check destination permissions and symlinks"

        print(message, file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    sys.exit(main())
