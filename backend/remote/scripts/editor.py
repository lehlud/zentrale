"""Install and supervise the headless VS Code agent on the SSH host.

The browser workbench is served by Zentrale, never by this process.
"""

import fcntl
import hashlib
import hmac
import json
import os
import platform
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.request
import zipfile
import select


def server_platform():
    architecture = {
        "x86_64": "x64",
        "amd64": "x64",
        "aarch64": "arm64",
        "arm64": "arm64",
    }.get(platform.machine().lower())
    system = {"Linux": "linux", "Darwin": "darwin"}.get(platform.system())
    if not architecture or not system:
        raise RuntimeError("VS Code remote agent requires 64-bit Linux or macOS")
    if system == "darwin" and architecture == "x64":
        return "server-darwin"
    return "server-" + system + "-" + architecture


def archive_path(directory, name):
    """Strip the enclosing distribution folder and confine every extracted path."""
    parts = name.replace("\\", "/").split("/", 1)
    if len(parts) == 1 or not parts[1]:
        return None
    relative = parts[1]
    target = os.path.abspath(os.path.join(directory, relative))
    if relative.startswith("/") or os.path.commonpath([directory, target]) != directory:
        raise RuntimeError("Remote agent archive contains an invalid path")
    return target


def extract_server(archive, directory):
    links = []
    total = 0

    def extract(name, size, mode, source, link=None):
        nonlocal total
        target = archive_path(directory, name)
        if target is None:
            return
        if link is not None:
            destination = os.path.abspath(os.path.join(os.path.dirname(target), link))
            if (
                os.path.isabs(link)
                or os.path.commonpath([directory, destination]) != directory
            ):
                raise RuntimeError("Remote agent archive contains an external link")
            links.append((target, link))
            return
        if source is None:
            os.makedirs(target, mode=0o700, exist_ok=True)
            return
        total += size
        if size < 0 or total > 1024 * 1024 * 1024:
            raise RuntimeError("Remote agent archive expands beyond the size limit")
        os.makedirs(os.path.dirname(target), mode=0o700, exist_ok=True)
        with source, open(target, "xb") as output:
            shutil.copyfileobj(source, output)
        os.chmod(target, 0o700 if mode & 0o111 else 0o600)

    if zipfile.is_zipfile(archive):
        with zipfile.ZipFile(archive) as bundle:
            for entry in bundle.infolist():
                mode = entry.external_attr >> 16
                if stat.S_ISLNK(mode):
                    extract(
                        entry.filename,
                        0,
                        mode,
                        None,
                        bundle.read(entry).decode("utf-8"),
                    )
                else:
                    extract(
                        entry.filename,
                        entry.file_size,
                        mode,
                        None if entry.is_dir() else bundle.open(entry),
                    )
    else:
        with tarfile.open(archive, "r:gz") as bundle:
            for entry in bundle:
                if entry.issym():
                    extract(entry.name, 0, entry.mode, None, entry.linkname)
                elif entry.isdir() or entry.isfile():
                    extract(
                        entry.name,
                        entry.size,
                        entry.mode,
                        bundle.extractfile(entry) if entry.isfile() else None,
                    )
                else:
                    raise RuntimeError(
                        "Remote agent archive contains an unsupported special file"
                    )

    # Deferred links prevent a later archive member from writing through one.
    for target, destination in links:
        parent = os.path.realpath(os.path.dirname(target))
        if os.path.commonpath([directory, parent]) != directory:
            raise RuntimeError(
                "Remote agent archive link parent leaves the installation"
            )
        os.makedirs(os.path.dirname(target), mode=0o700, exist_ok=True)
        os.symlink(destination, target)
    for target, _ in links:
        if os.path.commonpath([directory, os.path.realpath(target)]) != directory:
            raise RuntimeError("Remote agent archive link leaves the installation")


def install_server(release):
    target = server_platform()
    distribution = release["servers"].get(target)
    if not distribution:
        raise RuntimeError("No matching remote agent for " + target)

    cache_root = os.environ.get(
        "ZENTRALE_VSCODE_SERVER_CACHE",
        os.path.expanduser("~/.cache/zentrale/vscode-server"),
    )
    cache = os.path.join(cache_root, release["commit"], target)
    os.makedirs(cache, mode=0o700, exist_ok=True)
    installed = os.path.join(cache, "server")
    binary = os.path.join(installed, "bin", "code-server")
    with open(os.path.join(cache, "install.lock"), "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if os.path.isfile(binary) and os.access(binary, os.X_OK):
            return binary
        print("Preparing the VS Code remote agent on the SSH machine…", flush=True)
        with tempfile.TemporaryDirectory(prefix="download-", dir=cache) as download:
            archive = os.path.join(download, "server.archive")
            digest = hashlib.sha256()
            with (
                urllib.request.urlopen(distribution["url"], timeout=60) as response,
                open(archive, "wb") as output,
            ):
                total = 0
                while True:
                    chunk = response.read(1024 * 1024)
                    if not chunk:
                        break
                    total += len(chunk)
                    if total > 512 * 1024 * 1024:
                        raise RuntimeError(
                            "Remote agent download exceeds the size limit"
                        )
                    digest.update(chunk)
                    output.write(chunk)
            if digest.hexdigest() != distribution["sha256"]:
                raise RuntimeError("VS Code remote agent checksum mismatch")

            extracted = os.path.join(download, "server")
            os.makedirs(extracted, mode=0o700)
            extract_server(archive, extracted)
            with open(os.path.join(extracted, "product.json")) as source:
                if json.load(source).get("commit") != release["commit"]:
                    raise RuntimeError(
                        "VS Code workbench and remote agent versions differ"
                    )
            if not os.path.isfile(os.path.join(extracted, "bin", "code-server")):
                raise RuntimeError("Remote agent archive has no executable")
            if os.path.exists(installed):
                shutil.rmtree(installed)
            os.rename(extracted, installed)
    return binary


def main():
    os.umask(0o077)
    base_path, workspace, manifest = sys.argv[1:4]
    guest = json.loads(sys.argv[4]) if len(sys.argv) > 4 else None
    binary = install_server(json.loads(manifest))
    os.makedirs(workspace, exist_ok=True)
    stopped = threading.Event()

    def watch_connection():
        # EOF on the SSH control channel also terminates the agent process group.
        sys.stdin.buffer.read(1)
        stopped.set()

    threading.Thread(target=watch_connection, daemon=True).start()
    with tempfile.TemporaryDirectory(prefix="ze-", dir="/tmp") as runtime:
        socket_path = os.path.join(runtime, "vscode.sock")
        data_directory = os.environ.get(
            "ZENTRALE_VSCODE_SERVER_DATA",
            os.path.expanduser("~/.local/share/zentrale/vscode"),
        )
        command = [
            binary,
            "--start-server",
            "--socket-path",
            socket_path,
            "--server-base-path",
            base_path,
            "--without-connection-token",
            "--server-data-dir",
            data_directory,
            "--accept-server-license-terms",
        ]
        process = subprocess.Popen(
            command, stdin=subprocess.DEVNULL, start_new_session=True
        )
        try:
            deadline = time.monotonic() + 180
            while not stopped.is_set():
                if process.poll() is not None:
                    raise RuntimeError(
                        "VS Code remote agent exited before becoming ready"
                    )
                try:
                    with socket.socket(socket.AF_UNIX) as probe:
                        probe.connect(socket_path)
                    break
                except OSError:
                    if time.monotonic() > deadline:
                        raise RuntimeError(
                            "VS Code remote agent did not become ready within three minutes"
                        )
                    stopped.wait(0.1)

            if not stopped.is_set() and guest:
                relay = GuestRelay(socket_path, guest["token"])
                relay.start()
            if not stopped.is_set():
                print(
                    "ZENTRALE_EDITOR_READY " + json.dumps({"socketPath": socket_path}),
                    flush=True,
                )
            while not stopped.wait(0.5):
                if process.poll() is not None:
                    raise RuntimeError("VS Code remote agent stopped unexpectedly")
        finally:
            if guest and "relay" in locals():
                relay.close()
            try:
                os.killpg(process.pid, signal.SIGTERM)
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            except ProcessLookupError:
                pass


class GuestRelay:
    """Forward authenticated streams to the guest-local server Unix socket.

    A loopback QEMU forward is reachable by other SSH-host users. Keep the
    server behind a separate random session secret without exposing that secret
    to the browser or changing VS Code's workspace protocol.
    """

    def __init__(self, socket_path, token, port=9842):
        self.socket_path, self.token = socket_path, token.encode("ascii")
        self.listener = socket.socket()
        self.listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.listener.bind(("0.0.0.0", port))
        self.listener.listen(32)
        self.stopped = threading.Event()

    def start(self):
        threading.Thread(target=self.accept, daemon=True).start()

    def accept(self):
        while not self.stopped.is_set():
            try:
                connection, _ = self.listener.accept()
            except OSError:
                return
            threading.Thread(
                target=self.forward, args=(connection,), daemon=True
            ).start()

    def forward(self, connection):
        with connection, socket.socket(socket.AF_UNIX) as server:
            try:
                connection.settimeout(5)
                received = bytearray()
                while len(received) <= 128:
                    byte = connection.recv(1)
                    if not byte or byte == b"\n":
                        break
                    received.extend(byte)
                if not hmac.compare_digest(received, self.token):
                    return

                server.connect(self.socket_path)
                connection.sendall(b"OK\n")
                connection.settimeout(None)

                while not self.stopped.is_set():
                    readable, _, _ = select.select([connection, server], [], [], 1)
                    for source in readable:
                        data = source.recv(65536)
                        if not data:
                            return
                        (server if source is connection else connection).sendall(data)
            except OSError:
                return

    def close(self):
        self.stopped.set()
        try:
            self.listener.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        self.listener.close()


def terminate(_signal, _frame):
    raise SystemExit(0)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGHUP, terminate)
    try:
        main()
    except Exception as error:
        print(
            "VS Code remote agent startup failed: " + str(error),
            file=sys.stderr,
            flush=True,
        )
        raise SystemExit(1)
