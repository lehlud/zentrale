"""Real PTY protocol coverage; no VM, SSH daemon or third-party packages."""
import base64
import json
import os
from pathlib import Path
import selectors
import subprocess
import sys
import time
from types import SimpleNamespace
import unittest


class TerminalTest(unittest.TestCase):
    def start(self, command="exec /bin/sh -i", cols=80, rows=24):
        process = subprocess.Popen(
            [sys.executable, "-I", "-u", str(Path(__file__).with_name("terminal.py")),
             base64.b64encode(command.encode()).decode(), "--protocol"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(self.close, process)
        self.send(process, {"cols": cols, "rows": rows})
        self.until(process, b"\x00ZENTRALE_TERMINAL_READY\n")
        return process

    def close(self, process):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
        for stream in (process.stdin, process.stdout, process.stderr):
            stream.close()

    def send(self, process, frame):
        process.stdin.write(json.dumps(frame).encode() + b"\n")
        process.stdin.flush()

    def input(self, process, data):
        self.send(process, {"type": "input", "data": base64.b64encode(data).decode()})

    def until(self, process, expected):
        # A read may contain READY plus the next response. Consume only through
        # this marker; chunk boundaries must not decide whether later waits pass.
        output = getattr(process, "_test_terminal_output", bytearray())
        process._test_terminal_output = output
        deadline = time.monotonic() + 5
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            while expected not in output and time.monotonic() < deadline:
                if not selector.select(max(0, deadline - time.monotonic())):
                    break
                data = os.read(process.stdout.fileno(), 65536)
                if not data:
                    break
                output.extend(data)
        self.assertIn(expected, output, bytes(output))
        end = output.index(expected) + len(expected)
        consumed = bytes(output[:end])
        del output[:end]
        return consumed

    def test_geometry_input_resize_ctrl_c_and_exit(self):
        process = self.start(cols=93, rows=37)
        self.input(process, b"stty size; printf 'initial-done\\n'\n")
        self.assertIn(b"37 93", self.until(process, b"initial-done\r\n"))
        self.send(process, {"type": "resize", "cols": 71, "rows": 29})
        self.input(process, b"stty size; printf 'resize-done\\n'\n")
        self.assertIn(b"29 71", self.until(process, b"resize-done\r\n"))
        self.input(process, b"sleep 30\n")
        time.sleep(0.1)
        self.input(process, b"\x03")
        self.input(process, b"printf 'interrupt-done\\n'\n")
        self.until(process, b"interrupt-done\r\n")
        self.input(process, b"exit 7\n")
        self.assertEqual(process.wait(timeout=5), 7)

    def test_disconnect_kills_child(self):
        process = self.start("sleep 30")
        process.stdin.close()
        self.assertIsNotNone(process.wait(timeout=5))

    def test_invalid_resize_rejected(self):
        process = self.start()
        self.send(process, {"type": "resize", "cols": 0, "rows": 24})
        self.assertNotEqual(process.wait(timeout=5), 0)

    def test_binary_input_output_is_lossless(self):
        process = self.start("stty raw -echo; printf raw-mode-ready; dd bs=1 count=3 2>/dev/null")
        self.until(process, b"raw-mode-ready")
        self.input(process, b"\x00\xffx")
        self.until(process, b"\x00\xffx")
        self.assertEqual(process.wait(timeout=5), 0)

    def test_existing_raw_relay_contract_is_unchanged(self):
        result = subprocess.run(
            [sys.executable, "-I", "-u", str(Path(__file__).with_name("terminal.py")),
             base64.b64encode(b"stty size").decode()],
            input=b"", capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, b"32 120\r\n")

    def test_until_preserves_coalesced_output_for_the_next_marker(self):
        reader, writer = os.pipe()
        with os.fdopen(reader, "rb", buffering=0) as stream:
            try:
                os.write(writer, b"first-marker\nsecond-marker\n")
            finally:
                os.close(writer)
            process = SimpleNamespace(stdout=stream)
            self.until(process, b"first-marker\n")
            self.assertEqual(self.until(process, b"second-marker\n"), b"second-marker\n")


if __name__ == "__main__":
    unittest.main()
