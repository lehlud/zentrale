"""Bootstrap/attach one instance's daemon, then relay framed bytes over SSH."""

import fcntl
import hashlib
import json
import os
from pathlib import Path
import select
import socket
import stat
import subprocess
import sys
import time


def bridge(source, instance):
    # A fixed-size socket path works with long HOME directories. Never accept
    # another user's directory, symlinks, or a publicly accessible directory.
    key = hashlib.sha256(instance.encode()).hexdigest()[:24]
    directory = Path("/tmp") / ("zentrale-daemon-" + key)
    directory.mkdir(mode=0o700, exist_ok=True)
    info = directory.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise RuntimeError("daemon directory is unsafe or belongs to another SSH account; use one SSH account per Zentrale and host")
    line = bytearray()
    while not line.endswith(b"\n"):
        value = os.read(0, 1)
        if not value or len(line) >= 8192:
            raise RuntimeError("invalid daemon credentials frame")
        line.extend(value)
    config = json.loads(line)
    token = config["token"]
    with open(directory / "bootstrap.lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        token_file = directory / "token"
        if token_file.exists():
            if token_file.read_text() != token:
                raise RuntimeError("daemon instance credentials changed")
        else:
            descriptor = os.open(token_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "w") as target:
                target.write(token)
        client = socket.socket(socket.AF_UNIX)
        try:
            client.connect(str(directory / "socket"))
        except (FileNotFoundError, ConnectionRefusedError):
            client.close()
            with open(directory / "daemon.log", "ab", buffering=0) as log:
                subprocess.Popen([sys.executable, "-c", source, str(directory)], stdin=subprocess.DEVNULL,
                                 stdout=log, stderr=log, start_new_session=True, close_fds=True)
            deadline = time.monotonic() + 10
            while True:
                client = socket.socket(socket.AF_UNIX)
                try:
                    client.connect(str(directory / "socket"))
                    break
                except (FileNotFoundError, ConnectionRefusedError):
                    client.close()
                    if time.monotonic() >= deadline:
                        raise RuntimeError("daemon startup timed out; inspect " + str(directory / "daemon.log"))
                    time.sleep(0.05)
    # Credential input is unbuffered, so it cannot consume a protocol frame.
    while True:
        readable, _, _ = select.select([0, client], [], [])
        if 0 in readable:
            data = os.read(0, 65536)
            if not data:
                return
            client.sendall(data)
        if client in readable:
            data = client.recv(65536)
            if not data:
                return
            view = memoryview(data)
            while view:
                view = view[os.write(1, view):]
