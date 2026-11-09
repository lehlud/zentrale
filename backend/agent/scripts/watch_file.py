"""Invalidate a repeated-read grant after an external file or directory change."""

import ctypes
import os
import struct
import sys


def main():
    filename = os.path.abspath(sys.argv[1])
    directory, basename = os.path.split(filename)
    libc = ctypes.CDLL("libc.so.6", use_errno=True)
    descriptor = libc.inotify_init1(os.O_CLOEXEC)
    if descriptor < 0:
        raise OSError(ctypes.get_errno(), "inotify_init1 failed")

    # Changes, replacement, removal and changes to the watched directory itself.
    mask = (
        0x00000002
        | 0x00000004
        | 0x00000008
        | 0x00000040
        | 0x00000080
        | 0x00000200
        | 0x00000400
        | 0x00000800
    )
    try:
        watch = libc.inotify_add_watch(descriptor, os.fsencode(directory), mask)
        if watch < 0:
            raise OSError(ctypes.get_errno(), "inotify_add_watch failed")
        print("ready", flush=True)
        while True:
            events = os.read(descriptor, 65536)
            offset = 0
            while offset < len(events):
                _, event_mask, _, name_length = struct.unpack_from(
                    "iIII", events, offset
                )
                name_start = offset + 16
                name = events[name_start : name_start + name_length].split(b"\0", 1)[0]
                offset = name_start + name_length

                directory_changed = event_mask & (0x00000400 | 0x00000800)
                queue_overflowed = event_mask & 0x00004000
                if (
                    os.fsdecode(name) == basename
                    or directory_changed
                    or queue_overflowed
                ):
                    print("changed", flush=True)
                    return
    finally:
        os.close(descriptor)


if __name__ == "__main__":
    main()
