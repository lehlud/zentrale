"""Keep bulk move data off the daemon's bounded command-output log."""

import shutil
import signal
import socket
import subprocess
import sys
import tempfile
from pathlib import Path


def serve(mode, command):
    # Both endpoints authenticate using the existing SSH account. Other
    # accounts cannot connect to a socket in this private directory.
    with tempfile.TemporaryDirectory(prefix="zentrale-transfer-", dir="/tmp") as directory:
        address = str(Path(directory) / "stream")
        with socket.socket(socket.AF_UNIX) as listener:
            listener.bind(address)
            listener.listen(1)
            listener.settimeout(60)
            print(address, flush=True)
            peer, _ = listener.accept()
            with peer:
                send = mode == "send"
                return subprocess.run(
                    ["/bin/sh", "-c", command],
                    stdin=subprocess.DEVNULL if send else peer,
                    stdout=peer if send else subprocess.DEVNULL,
                ).returncode


def relay(mode, address):
    with socket.socket(socket.AF_UNIX) as peer:
        peer.settimeout(30)
        peer.connect(address)
        peer.settimeout(None)
        if mode == "send":
            with peer.makefile("rb") as source:
                shutil.copyfileobj(source, sys.stdout.buffer, 128 * 1024)
            sys.stdout.buffer.flush()
        else:
            with peer.makefile("wb") as destination:
                shutil.copyfileobj(sys.stdin.buffer, destination, 128 * 1024)
            peer.shutdown(socket.SHUT_WR)


def cancel(*_):
    raise SystemExit(143)


if __name__ == "__main__":
    action, mode, value = sys.argv[1:]
    if mode not in ("send", "receive"):
        sys.exit("Invalid transfer stream direction")
    signal.signal(signal.SIGTERM, cancel)
    if action == "serve":
        sys.exit(serve(mode, value))
    if action == "relay":
        relay(mode, value)
    else:
        sys.exit("Invalid transfer stream action")
