"""Bootstrap tests use a local stand-in executable, without a VS Code download."""

import contextlib
import hashlib
import importlib.util
import io
import json
import os
import socket
import threading
from pathlib import Path
import select
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name("editor.py")
specification = importlib.util.spec_from_file_location("remote_editor", SCRIPT)
editor = importlib.util.module_from_spec(specification)
specification.loader.exec_module(editor)


class EditorBootstrapTests(unittest.TestCase):
    def test_dev_server_can_listen_only_on_ipv6_loopback(self):
        try:
            server = socket.socket(socket.AF_INET6)
            server.bind(("::1", 0))
        except OSError:
            self.skipTest("IPv6 loopback is unavailable")
        self.addCleanup(server.close)
        server.listen()
        with editor.connect_loopback(server.getsockname()[1]) as client:
            with server.accept()[0] as accepted:
                client.sendall(b"IPv6 preview")
                self.assertEqual(accepted.recv(64), b"IPv6 preview")

    def test_guest_relay_authenticates_and_forwards_only_a_loopback_port(self):
        server = socket.socket()
        self.addCleanup(server.close)
        server.bind(("127.0.0.1", 0))
        server.listen()
        target_port = server.getsockname()[1]
        accepted = threading.Event()

        def echo():
            with server.accept()[0] as connection:
                accepted.set()
                while data := connection.recv(65536):
                    connection.sendall(data)

        threading.Thread(target=echo, daemon=True).start()
        relay = editor.GuestRelay("/does/not/exist", "session-secret", port=0)
        self.addCleanup(relay.close)
        relay.start()
        address = ("127.0.0.1", relay.listener.getsockname()[1])
        for authentication in [
            f"wrong-secret {target_port}\n",
            "session-secret 0\n",
            "session-secret 65536\n",
            "session-secret 9842\n",
            "session-secret example.org:1235\n",
        ]:
            with self.subTest(authentication=authentication):
                with socket.create_connection(address, timeout=2) as client:
                    client.sendall(authentication.encode())
                    self.assertEqual(client.recv(3), b"")
        self.assertFalse(accepted.is_set())

        with socket.create_connection(address, timeout=2) as client:
            client.sendall(f"session-secret {target_port}\n".encode())
            self.assertEqual(client.recv(3), b"OK\n")
            payload = b"GET /api/example HTTP/1.1\r\nHost: localhost\r\n\r\n"
            client.sendall(payload)
            self.assertEqual(client.recv(65536), payload)
        self.assertTrue(accepted.wait(2))

    def test_guest_relay_authenticates_before_forwarding_binary_streams(self):
        with tempfile.TemporaryDirectory() as directory:
            socket_path = str(Path(directory) / "server.sock")
            server = socket.socket(socket.AF_UNIX)
            self.addCleanup(server.close)
            server.bind(socket_path)
            server.listen()
            accepted = threading.Event()

            def echo():
                with server.accept()[0] as connection:
                    accepted.set()
                    while data := connection.recv(65536):
                        connection.sendall(data)

            threading.Thread(target=echo, daemon=True).start()
            relay = editor.GuestRelay(socket_path, "private-session-secret", port=0)
            self.addCleanup(relay.close)
            port = relay.listener.getsockname()[1]
            relay.start()
            with socket.create_connection(("127.0.0.1", port), timeout=2) as attacker:
                attacker.sendall(b"incorrect-secret\n")
                self.assertEqual(attacker.recv(3), b"")
            self.assertFalse(
                accepted.is_set(), "unauthenticated client reached VS Code"
            )

            with socket.create_connection(("127.0.0.1", port), timeout=2) as client:
                client.sendall(b"private-session-secret\n")
                self.assertEqual(client.recv(3), b"OK\n")
                payload = bytes(range(256)) * 128
                client.sendall(payload)
                received = bytearray()
                while len(received) < len(payload):
                    received.extend(client.recv(65536))
                self.assertEqual(received, payload)
            self.assertTrue(accepted.wait(2))

    def test_published_server_archive(self):
        archive = os.environ.get("ZENTRALE_VSCODE_SERVER_ARCHIVE")
        if not archive:
            self.skipTest(
                "set ZENTRALE_VSCODE_SERVER_ARCHIVE for the published archive test"
            )
        release = json.loads(
            (SCRIPT.parents[2] / "vscode" / "release.json").read_text()
        )
        with open(archive, "rb") as source:
            digest = hashlib.sha256()
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
            self.assertEqual(
                digest.hexdigest(),
                release["servers"][editor.server_platform()]["sha256"],
            )
        with tempfile.TemporaryDirectory() as directory:
            editor.extract_server(archive, directory)
            product = json.loads((Path(directory) / "product.json").read_text())
            self.assertEqual(product["commit"], release["commit"])
            self.assertTrue(os.access(Path(directory) / "bin" / "code-server", os.X_OK))

    def test_pinned_agent_is_verified_and_installed_atomically(self):
        archive = io.BytesIO()
        content = b"#!/bin/sh\nexit 0\n"
        with tarfile.open(fileobj=archive, mode="w:gz") as bundle:
            for name, data, mode in [
                ("server/bin/code-server", content, 0o700),
                ("server/product.json", b'{"commit":"test-commit"}', 0o600),
            ]:
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode = len(data), mode
                bundle.addfile(entry, io.BytesIO(data))
        release = {
            "commit": "test-commit",
            "servers": {
                "server-linux-arm64": {
                    "url": "https://example.test/agent.tar.gz",
                    "sha256": hashlib.sha256(archive.getvalue()).hexdigest(),
                }
            },
        }
        with tempfile.TemporaryDirectory() as directory:
            cache = str(Path(directory) / "cache")
            with (
                patch.dict(editor.os.environ, {"ZENTRALE_VSCODE_SERVER_CACHE": cache}),
                patch.object(editor.platform, "system", return_value="Linux"),
                patch.object(editor.platform, "machine", return_value="aarch64"),
                patch.object(
                    editor.urllib.request,
                    "urlopen",
                    return_value=io.BytesIO(archive.getvalue()),
                ) as download,
                contextlib.redirect_stdout(io.StringIO()),
            ):
                binary = editor.install_server(release)
                self.assertEqual(binary, editor.install_server(release))
                download.assert_called_once_with(
                    "https://example.test/agent.tar.gz", timeout=60
                )
                self.assertEqual(stat.S_IMODE(os.stat(binary).st_mode), 0o700)
                self.assertEqual(Path(binary).read_bytes(), content)

    def test_archive_cannot_escape_installation_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name, link in [
                ("server/../../outside", None),
                ("server/link", "../../outside"),
            ]:
                archive = root / "server.tar.gz"
                with tarfile.open(archive, "w:gz") as bundle:
                    entry = tarfile.TarInfo(name)
                    if link:
                        entry.type, entry.linkname = tarfile.SYMTYPE, link
                    bundle.addfile(entry)
                with self.assertRaises(RuntimeError):
                    editor.extract_server(archive, str(root / "installation"))
                self.assertFalse((root / "outside").exists())

    def test_ssh_eof_stops_server_and_removes_private_socket(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / "fake-code"
            pid_file = root / "pid"
            workspace = root / "workspace"
            binary.write_text(
                "#!"
                + sys.executable
                + "\n"
                + """
import os, signal, socket, sys
assert '--start-server' in sys.argv
assert 'serve-web' not in sys.argv
path = sys.argv[sys.argv.index('--socket-path') + 1]
with open(os.environ['ZENTRALE_EDITOR_TEST_PID'], 'w') as output:
    output.write(str(os.getpid()))
server = socket.socket(socket.AF_UNIX)
server.bind(path)
server.listen()
while True:
    peer, _ = server.accept()
    peer.close()
"""
            )
            binary.chmod(0o700)
            harness = """
import importlib.util, sys
spec = importlib.util.spec_from_file_location('editor', sys.argv[1])
editor = importlib.util.module_from_spec(spec)
spec.loader.exec_module(editor)
binary, workspace = sys.argv[2:4]
editor.install_server = lambda release: binary
sys.argv = ['editor.py', '/ide/project/session', workspace, '{}']
editor.main()
"""
            environment = {**os.environ, "ZENTRALE_EDITOR_TEST_PID": str(pid_file)}
            process = subprocess.Popen(
                [
                    sys.executable,
                    "-u",
                    "-c",
                    harness,
                    str(SCRIPT),
                    str(binary),
                    str(workspace),
                ],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=environment,
            )
            try:
                readable, _, _ = select.select([process.stdout], [], [], 8)
                self.assertTrue(readable, "editor supervisor did not become ready")
                line = process.stdout.readline().decode()
                self.assertTrue(line.startswith("ZENTRALE_EDITOR_READY "), line)
                socket_path = Path(json.loads(line.split(" ", 1)[1])["socketPath"])
                self.assertEqual(stat.S_IMODE(socket_path.parent.stat().st_mode), 0o700)
                self.assertTrue(workspace.is_dir())

                process.stdin.close()
                self.assertEqual(
                    process.wait(timeout=8), 0, process.stderr.read().decode()
                )
                self.assertFalse(socket_path.parent.exists())
                with self.assertRaises(ProcessLookupError):
                    os.kill(int(pid_file.read_text()), 0)
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
                process.stdout.close()
                process.stderr.close()


if __name__ == "__main__":
    unittest.main()
