"""Exercise the real guest/host stream pumps without booting KVM or mounting."""

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch


DIRECTORY = Path(__file__).parent


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, DIRECTORY / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


streams = load("mailbox_streams", "streams.py")
guest = load("stream_guest_init", "init.py")
runner = load("stream_host_runner", "runner.py")


class StreamingTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.control = Path(temporary.name)
        (self.control / "streams.py").write_bytes(
            (DIRECTORY / "streams.py").read_bytes()
        )
        self.job = self.control / "requests" / "job"
        self.job.mkdir(parents=True)

    def test_guest_preserves_large_regular_and_persistent_protocol_streams(self):
        for protocol in (False, True):
            with self.subTest(protocol=protocol):
                self.exercise_guest(protocol)

    def exercise_guest(self, protocol):
        job = self.job / str(protocol)
        job.mkdir()
        (job / "request.json").write_text(json.dumps({"protocolIO": protocol}))
        # Protocol crosses the former 16 MiB lifetime input limit; both cases
        # cross the former 4 MiB output limit. Real child pipes carry the bytes.
        block = bytes(range(256)) * 256
        count = 384 if protocol else 96
        expected = hashlib.sha256(block * count).hexdigest()
        script = (
            "import hashlib, sys\n"
            "digest = hashlib.sha256()\n"
            "while data := sys.stdin.buffer.read1(65536):\n"
            " digest.update(data)\n"
            " sys.stdout.buffer.write(data)\n"
            " sys.stdout.buffer.flush()\n"
            "sys.stderr.write(digest.hexdigest())\n"
        )
        original_popen = subprocess.Popen
        deadline = time.monotonic() + 10
        failures, results = [], {}

        def receive(name):
            try:
                stream = streams.MailboxStream(job, name)
                digest, received, text = hashlib.sha256(), 0, bytearray()
                while time.monotonic() < deadline:
                    data = stream.read()
                    if data is None:
                        time.sleep(0.001)
                        continue
                    if not data:
                        results[name] = (digest.hexdigest(), received, text)
                        return
                    digest.update(data)
                    received += len(data)
                    if name == "stderr":
                        text.extend(data)
                raise TimeoutError(name)
            except BaseException as error:
                failures.append(error)

        readers = [
            threading.Thread(target=receive, args=(name,))
            for name in ("stdout", "stderr")
        ]
        for reader in readers:
            reader.start()
        if not protocol:
            (job / "stdin").write_bytes(block * count)
            (job / "stdin.eof").touch()
        with (
            patch.object(guest, "CONTROL", str(self.control)),
            patch.object(
                guest.subprocess,
                "Popen",
                side_effect=lambda _args, **kwargs: original_popen(
                    [sys.executable, "-u", "-c", script], **kwargs
                ),
            ),
        ):
            worker = threading.Thread(target=guest.run_job, args=(job,))
            worker.start()
            if protocol:
                input_stream = streams.MailboxStream(
                    job, "stdin", lambda: time.monotonic() > deadline
                )
                for _ in range(count):
                    input_stream.write(block)
                input_stream.close()
            worker.join(timeout=10)
            self.assertFalse(worker.is_alive(), "guest stream did not finish")

        for reader in readers:
            reader.join(timeout=10)
            self.assertFalse(reader.is_alive())
        self.assertEqual(failures, [])
        self.assertEqual((job / "status").read_text(), "0")
        self.assertEqual(results["stdout"][:2], (expected, len(block) * count))
        self.assertEqual(results["stderr"][2].decode(), expected)
        self.assertEqual(list(job.glob("*.chunk")), [])

    def test_backpressure_bounds_storage_and_cancellation_unblocks_writer(self):
        cancelled = threading.Event()
        failures = []
        stream = streams.MailboxStream(self.job, "stdout", cancelled.is_set)

        def fill():
            try:
                stream.write(b"x" * 8 * 1024 * 1024)
            except InterruptedError as error:
                failures.append(error)

        writer = threading.Thread(target=fill)
        writer.start()
        deadline = time.monotonic() + 3
        while (
            len(list(self.job.glob("*.chunk"))) < stream.QUEUED_CHUNKS
            and time.monotonic() < deadline
        ):
            time.sleep(0.005)
        files = list(self.job.glob("*.chunk"))
        self.assertTrue(writer.is_alive(), "writer silently discarded excess bytes")
        self.assertEqual(len(files), stream.QUEUED_CHUNKS)
        self.assertLessEqual(
            sum(file.stat().st_size for file in files), 2 * 1024 * 1024
        )

        cancelled.set()
        writer.join(timeout=1)
        self.assertFalse(writer.is_alive())
        self.assertEqual(len(failures), 1)

    def test_host_forwards_large_output_even_when_ssh_writes_are_partial(self):
        (self.job / "request.json").write_text("{}")
        (self.control / "supervisor.pid").write_text(
            json.dumps(runner.process_reference())
        )
        block = b"startup\n" + bytes(range(256)) * 256 + b"\nfinal diagnostic\n"
        expected = hashlib.sha256(block * 100).hexdigest()
        digest, errors = hashlib.sha256(), []

        def produce():
            try:
                output = streams.MailboxStream(self.job, "stdout")
                for _ in range(100):
                    output.write(block)
                output.close()
                streams.MailboxStream(self.job, "stderr").close()
                (self.job / "status").write_text("0")
            except BaseException as error:
                errors.append(error)

        def partial_write(descriptor, data):
            accepted = min(10007, len(data))
            if descriptor == 1:
                digest.update(data[:accepted])
            return accepted

        writer = threading.Thread(target=produce)
        writer.start()
        original_read = os.read
        with (
            patch.object(
                runner.os,
                "read",
                side_effect=lambda fd, size: (
                    b"" if fd == 0 else original_read(fd, size)
                ),
            ),
            patch.object(runner.os, "write", side_effect=partial_write),
        ):
            self.assertEqual(runner.run_invocation(self.job), 0)
        writer.join(timeout=3)
        self.assertFalse(writer.is_alive())
        self.assertEqual(errors, [])
        self.assertEqual(digest.hexdigest(), expected)
        self.assertTrue((self.job / "acknowledged").exists())

    def test_host_reports_failure_before_guest_streams_start(self):
        (self.job / "request.json").write_text("{}")
        (self.job / "status").write_text("125")
        (self.job / "stderr").write_text("MicroVM task failed: missing guest runtime")
        diagnostic = bytearray()

        def write(descriptor, data):
            if descriptor == 2:
                diagnostic.extend(data)
            return len(data)

        original_read = os.read
        with (
            patch.object(
                runner.os,
                "read",
                side_effect=lambda fd, size: (
                    b"" if fd == 0 else original_read(fd, size)
                ),
            ),
            patch.object(runner.os, "write", side_effect=write),
        ):
            self.assertEqual(runner.run_invocation(self.job), 125)
        self.assertIn(b"missing guest runtime", diagnostic)


if __name__ == "__main__":
    unittest.main()
