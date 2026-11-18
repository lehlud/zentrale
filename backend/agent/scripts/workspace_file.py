"""Stream a regular project file without following symbolic links."""

import errno
import os
import stat
import sys


def send_file(root, relative, limit):
    components = relative.split("/")
    if any(part in ("", ".", "..") for part in components):
        print("missing", flush=True)
        return

    directory = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    try:
        parent = os.open(root, directory)
        try:
            for component in components[:-1]:
                child = os.open(component, directory, dir_fd=parent)
                os.close(parent)
                parent = child
            flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK
            descriptor = os.open(components[-1], flags, dir_fd=parent)
        finally:
            os.close(parent)
    except OSError as error:
        if error.errno not in (
            errno.ENOENT, errno.ENOTDIR, errno.ELOOP, errno.EACCES, errno.ENAMETOOLONG,
        ):
            raise
        print("missing", flush=True)
        return

    with os.fdopen(descriptor, "rb") as source:
        metadata = os.fstat(source.fileno())
        if not stat.S_ISREG(metadata.st_mode):
            print("missing", flush=True)
        elif metadata.st_size > limit:
            print("too_large", flush=True)
        else:
            remaining = metadata.st_size
            print(remaining, flush=True)
            while remaining:
                data = source.read(min(remaining, 65536))
                if not data:
                    break
                sys.stdout.buffer.write(data)
                remaining -= len(data)


send_file(sys.argv[1], sys.argv[2], int(sys.argv[3]))
