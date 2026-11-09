"""Relay a controlling terminal without requiring an SSH PTY on the host.

Managed VMs call this before chroot/drop-privileges. Other sandboxes run the
same trusted relay using their Python runtime. No command is evaluated here.
"""

import ctypes
import errno
import fcntl
import os
import pty
import select
import signal
import sys
import struct
import termios


def run_terminal(child, namespace_init=False):
    parent = os.getpid()
    pid, master = pty.fork()
    if pid == 0:
        try:
            if sys.platform == "linux":
                libc = ctypes.CDLL(None, use_errno=True)
                if libc.prctl(1, signal.SIGKILL, 0, 0, 0) != 0:
                    os._exit(125)
                expected_parent = (
                    os.getppid() == parent or namespace_init and os.getppid() == 0
                )
                if not expected_parent:
                    os._exit(125)
            os.environ["TERM"] = "xterm-256color"
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 120, 0, 0))
            child()
        except BaseException as error:
            print("Terminal startup failed: " + str(error), file=sys.stderr, flush=True)
        os._exit(125)

    def stop(signum, frame):
        try:
            os.killpg(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass

    previous = {
        number: signal.signal(number, stop)
        for number in (signal.SIGTERM, signal.SIGHUP)
    }
    pending = bytearray()
    input_open = True
    os.set_blocking(master, False)
    try:
        while True:
            readable = [master]
            if input_open and len(pending) < 65536:
                readable.append(0)
            ready, writable, _ = select.select(
                readable, [master] if pending else [], []
            )
            if 0 in ready:
                data = os.read(0, min(65536 - len(pending), 65536))
                if data:
                    pending.extend(data)
                else:
                    pending.extend(b"\x04")
                    input_open = False

            if master in writable:
                try:
                    count = os.write(master, pending)
                    del pending[:count]
                except BlockingIOError:
                    pass

            if master in ready:
                try:
                    data = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                if not data:
                    break
                remaining = memoryview(data)
                while remaining:
                    count = os.write(1, remaining)
                    remaining = remaining[count:]

        _, status = os.waitpid(pid, 0)
        code = os.waitstatus_to_exitcode(status)
        return code if code >= 0 else 128 - code
    finally:
        stop(signal.SIGTERM, None)
        os.close(master)
        for number, handler in previous.items():
            signal.signal(number, handler)
        try:
            os.waitpid(pid, 0)
        except ChildProcessError:
            pass


if __name__ == "__main__":
    import base64

    command = base64.b64decode(sys.argv[1]).decode()
    sys.exit(run_terminal(lambda: os.execv("/bin/sh", ["/bin/sh", "-c", command])))
