import asyncio
import base64
import errno
import json
from pathlib import Path
import shlex
import tempfile
import unittest
from unittest.mock import patch

import daemon


class DaemonTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name)
        self.server = daemon.Daemon(self.directory, "private-test-token")

    async def asyncTearDown(self):
        await asyncio.gather(*(item.stop() for item in self.server.resources.values()))
        await asyncio.sleep(0.05)
        self.temporary.cleanup()

    async def request(self, operation, identity="job", **values):
        return await self.server.dispatch({"op": operation, "resource": identity,
                                           "generation": self.server.generation, **values})

    async def output(self, identity="job"):
        output, offset = bytearray(), 0
        while True:
            result = await self.request("read", identity, offset=offset)
            output.extend(base64.b64decode(result["data"]))
            offset = result["offset"]
            if result["eof"]:
                return bytes(output), result

    async def test_admission_and_input_replay_are_idempotent(self):
        counter = self.directory / "starts"
        command = "printf x >> %s; cat" % shlex.quote(str(counter))
        await self.request("create", kind="process", command=command, merge=True)
        await self.request("create", kind="process", command=command, merge=True)
        data = base64.b64encode(b"one input\n").decode()
        await self.request("write", offset=0, data=data)
        await self.request("write", offset=0, data=data)
        with self.assertRaisesRegex(ValueError, "conflicting input replay"):
            await self.request("write", offset=0, data=base64.b64encode(b"different").decode())
        await self.request("write", offset=10, eof=True)
        output, result = await asyncio.wait_for(self.output(), 5)
        self.assertEqual(counter.read_text(), "x")
        self.assertEqual(output, b"one input\n")
        self.assertEqual(result["code"], 0)
        with self.assertRaisesRegex(ValueError, "another operation"):
            await self.request("create", kind="process", command="false", merge=True)

    async def test_long_poll_does_not_block_other_actions(self):
        await self.request("create", kind="process", command="sleep 30", merge=True)
        pending = asyncio.create_task(self.request("read", offset=0))
        await asyncio.sleep(0.01)
        result = await asyncio.wait_for(self.request("status"), 0.5)
        self.assertEqual(result["state"], "running")
        await self.request("stop")
        await asyncio.wait_for(pending, 5)

    async def test_journal_failure_after_spawn_stops_the_process(self):
        original = daemon.Resource.journal
        def journal(resource):
            if resource.state == "running":
                raise OSError("disk full")
            return original(resource)
        with patch.object(daemon.Resource, "journal", journal):
            result = await asyncio.wait_for(self.request("create", kind="process", command="sleep 30", merge=True), 5)
        self.assertIn("disk full", result["error"])
        self.assertIsNotNone(self.server.resources["job"].process.returncode)

    async def test_output_storage_failure_stops_without_deadlocking(self):
        original = open
        original_journal = daemon.Resource.journal
        def open_output(path, *args, **kwargs):
            if str(path).endswith(".stdout"):
                raise OSError("disk full")
            return original(path, *args, **kwargs)
        def journal(resource):
            if resource.state == "exited":
                raise OSError("disk full")
            return original_journal(resource)
        with patch("builtins.open", open_output), patch.object(daemon.Resource, "journal", journal):
            await self.request("create", kind="process", command="yes large-output", merge=True)
            for _ in range(100):
                result = await self.request("status")
                if result["state"] == "exited": break
                await asyncio.sleep(0.01)
        self.assertEqual(result["state"], "exited")
        self.assertIn("output storage failed", result["error"])

    async def test_daemon_replacement_never_replays_uncertain_work(self):
        await self.request("create", kind="process", command="sleep 30", merge=True)
        replacement = daemon.Daemon(self.directory, "private-test-token")
        with self.assertRaisesRegex(ValueError, "generation changed"):
            await replacement.dispatch({"op": "create", "resource": "job", "generation": self.server.generation,
                                        "kind": "process", "command": "true"})
        with self.assertRaisesRegex(ValueError, "previous daemon"):
            await replacement.dispatch({"op": "create", "resource": "job", "generation": replacement.generation,
                                        "kind": "process", "command": "true"})

    async def test_disk_storage_preserves_admission_when_control_quota_is_full(self):
        await self.request("create", kind="process", command="true", merge=True)
        await asyncio.wait_for(self.output(), 5)
        storage = self.directory / "disk-storage"
        storage.mkdir(mode=0o700)
        self.server = daemon.Daemon(self.directory, "private-test-token", storage=storage)

        original = open
        def open_under_quota(path, *args, **kwargs):
            path = Path(path)
            if path.parent == self.directory and path.suffix in (".new", ".stdout", ".stderr"):
                raise OSError(errno.EDQUOT, "Disk quota exceeded")
            return original(path, *args, **kwargs)

        with patch("builtins.open", open_under_quota):
            with self.assertRaisesRegex(ValueError, "previous daemon"):
                await self.request("create", kind="process", command="true", merge=True)
            await self.request(
                "create", "fresh", kind="process",
                command='printf "%s" "$ZENTRALE_DAEMON_LEASE"', merge=True,
            )
            output, result = await asyncio.wait_for(self.output("fresh"), 5)
        self.assertEqual(result["code"], 0)
        self.assertEqual(output, str(self.directory / "lease").encode())
        self.assertTrue((storage / "fresh.json").is_file())
        self.assertTrue((storage / "fresh.stdout").is_file())
        self.assertFalse((self.directory / "fresh.json").exists())

    async def test_lease_expiry_stops_owned_processes(self):
        self.server.lease_seconds = 0.05
        await self.request("create", kind="process", command="sleep 30", merge=True)
        resource = self.server.resources["job"]
        await asyncio.wait_for(self.server.supervise(), 5)
        self.assertIsNotNone(resource.process.returncode)

    async def test_output_cursor_can_be_reattached_after_lost_reader(self):
        await self.request("create", kind="process", command="printf first; sleep 0.1; printf second", merge=True)
        first = await self.request("read", offset=0)
        self.assertEqual(base64.b64decode(first["data"]), b"first")
        # Reconstruct only an attachment: the command is never created again.
        await asyncio.sleep(0.2)
        second = await self.request("read", offset=first["offset"])
        self.assertEqual(base64.b64decode(second["data"]), b"second")
        replay = await self.request("read", offset=0)
        self.assertEqual(base64.b64decode(replay["data"]), b"firstsecond")

    async def test_output_overflow_terminates_without_wait_deadlock(self):
        with patch.object(daemon, "OUTPUT_LIMIT", 32):
            await self.request("create", kind="process", command="python3 -c 'print(\"x\" * 100000)'", merge=True)
            _, result = await asyncio.wait_for(self.output(), 5)
        self.assertIn("output limit exceeded", result["error"])
        self.assertEqual(result["state"], "exited")

    async def test_stop_before_start_cannot_launch_late_work(self):
        resource = daemon.Resource(self.server, "job", "fingerprint", "process")
        self.server.resources["job"] = resource
        resource.journal()
        await self.request("stop")
        marker = self.directory / "must-not-exist"
        await resource.start({"command": "touch " + shlex.quote(str(marker))})
        self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main()
