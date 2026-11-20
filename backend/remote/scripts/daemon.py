"""Private, reconnectable transport for one Zentrale installation.

SSH only carries authenticated frames. A lost bridge never owns a process's
lifetime. Admission, stdin receipts and output belong to this daemon instead.
"""

import asyncio
import base64
import contextlib
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import signal
import stat
import struct
import sys
import time
import uuid

VERSION = 1
LEASE_SECONDS = 15 * 60
FRAME_LIMIT = 1024 * 1024
CHUNK = 32768
OUTPUT_LIMIT = 64 * 1024 * 1024
TOTAL_OUTPUT_LIMIT = 256 * 1024 * 1024
RESOURCE_LIMIT = 256
IDENTIFIER = re.compile(r"^[a-zA-Z0-9_-]{1,100}$")


def atomic_json(path, value):
    temporary = path.with_suffix(".new")
    try:
        with open(temporary, "w", encoding="utf-8") as target:
            os.chmod(temporary, 0o600)
            json.dump(value, target, separators=(",", ":"))
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)
    descriptor = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def runtime_storage(directory, disk_root=Path("/var/tmp")):
    root = disk_root / ("zentrale-daemon-data-" + str(os.getuid()))
    storage = root / directory.name
    if not storage.exists() and not storage.is_symlink():
        if (not disk_root.is_dir() or not os.access(disk_root, os.W_OK | os.X_OK)
                or disk_root.stat().st_dev == directory.stat().st_dev):
            return directory

    for path in (root, storage):
        path.mkdir(mode=0o700, exist_ok=True)
        info = path.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise RuntimeError("unsafe daemon runtime storage: " + str(path))
    return storage


class Resource:
    def __init__(self, daemon, identity, fingerprint, kind):
        self.daemon, self.id, self.fingerprint, self.kind = daemon, identity, fingerprint, kind
        self.state, self.code, self.error = "admitted", None, ""
        self.writer, self.process = None, None
        self.input_offset, self.input_eof = 0, False
        self.receipts = {}
        self.input_lock = asyncio.Lock()
        self.start_lock = asyncio.Lock()
        self.changed = asyncio.Event()
        self.lengths = {"stdout": 0, "stderr": 0}
        self.finished_at = None
        self.last_contact = time.monotonic()
        self.idle_timeout = LEASE_SECONDS

    def journal(self):
        atomic_json(self.daemon.storage / (self.id + ".json"), {
            "id": self.id, "fingerprint": self.fingerprint, "kind": self.kind,
            "state": self.state, "code": self.code, "error": self.error,
            "generation": self.daemon.generation,
        })

    def info(self):
        return {"state": self.state, "code": self.code, "error": self.error,
                "inputOffset": self.input_offset, "inputEOF": self.input_eof}

    async def drain(self, reader, stream):
        path = self.daemon.storage / (self.id + "." + stream)
        output = None
        failed = False
        try:
            try:
                output = open(path, "ab", buffering=0)
                os.chmod(path, 0o600)
            except OSError as error:
                self.error = "daemon output storage failed: " + str(error)
                failed = True
                asyncio.create_task(self.stop())
            while True:
                chunk = await reader.read(CHUNK)
                if not chunk:
                    return
                if failed:
                    continue  # Keep draining so process.wait() cannot deadlock.
                if (sum(self.lengths.values()) + len(chunk) > OUTPUT_LIMIT
                        or self.daemon.output_bytes + len(chunk) > TOTAL_OUTPUT_LIMIT):
                    self.error = "daemon output limit exceeded; process stopped and output after the limit was discarded"
                    failed = True
                    asyncio.create_task(self.stop())
                    continue
                try:
                    written = output.write(chunk)
                    self.lengths[stream] += written
                    self.daemon.output_bytes += written
                    self.changed.set()
                    if written != len(chunk):
                        raise OSError("incomplete output write")
                except OSError as error:
                    self.error = "daemon output storage failed; process stopped: " + str(error)
                    failed = True
                    asyncio.create_task(self.stop())
        finally:
            if output is not None:
                output.close()

    async def start(self, request):
        async with self.start_lock:
            if self.state != "admitted":
                return self.info()
            try:
                if self.kind == "process":
                    self.process = await asyncio.create_subprocess_exec(
                        "/bin/sh", "-c", request["command"],
                        stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
                        stderr=(asyncio.subprocess.STDOUT if request.get("merge") else asyncio.subprocess.PIPE),
                        start_new_session=True,
                        env={**os.environ, "ZENTRALE_DAEMON_LEASE": str(self.daemon.directory / "lease")},
                    )
                    self.writer = self.process.stdin
                    self.idle_timeout = min(float(request.get("idleTimeout") or LEASE_SECONDS), LEASE_SECONDS)
                    self.state = "running"
                    asyncio.create_task(self.run_process(request.get("timeout", 0)))
                    self.journal()
                elif self.kind == "tunnel":
                    if request.get("network") == "unix":
                        reader, self.writer = await asyncio.wait_for(
                            asyncio.open_unix_connection(request["address"]), 15)
                    else:
                        reader, self.writer = await asyncio.wait_for(
                            asyncio.open_connection(request["host"], request["port"]), 15)
                    self.state = "running"
                    asyncio.create_task(self.run_tunnel(reader))
                    self.journal()
                else:
                    raise ValueError("unsupported resource kind")
            except Exception as error:
                self.error, self.state = str(error), "failed"
                await self.stop_started()
                with contextlib.suppress(OSError):
                    self.journal()
                self.changed.set()
            return self.info()

    async def run_process(self, timeout):
        streams = [asyncio.create_task(self.drain(self.process.stdout, "stdout"))]
        if self.process.stderr is not None:
            streams.append(asyncio.create_task(self.drain(self.process.stderr, "stderr")))
        try:
            if timeout:
                await asyncio.wait_for(self.process.wait(), min(float(timeout), 7 * 24 * 3600))
            else:
                await self.process.wait()
        except asyncio.TimeoutError:
            self.error = "remote command deadline exceeded"
            await self.stop()
        finally:
            await asyncio.gather(*streams, return_exceptions=True)
            self.code = self.process.returncode
            self.finish()

    async def run_tunnel(self, reader):
        try:
            await self.drain(reader, "stdout")
        except (OSError, asyncio.IncompleteReadError) as error:
            self.error = str(error)
        finally:
            self.finish()

    def finish(self):
        self.state = "exited"
        self.finished_at = time.monotonic()
        try:
            self.journal()
        except OSError as error:
            if not self.error:
                self.error = "daemon completion journal failed: " + str(error)
        self.changed.set()

    async def stop(self):
        async with self.start_lock:
            if self.state == "admitted":
                self.code = -15
                self.finish()
                return
            await self.stop_started()

    async def stop_started(self):
        if self.process is not None and self.process.returncode is None:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(self.process.pid, signal.SIGTERM)
            try:
                await asyncio.wait_for(self.process.wait(), 5)
            except asyncio.TimeoutError:
                with contextlib.suppress(ProcessLookupError):
                    os.killpg(self.process.pid, signal.SIGKILL)
                await self.process.wait()
        elif self.kind == "tunnel" and self.writer is not None:
            self.writer.close()

    async def write(self, request):
        data = base64.b64decode(request.get("data", ""), validate=True)
        if len(data) > CHUNK:
            raise ValueError("input frame too large")
        offset = request["offset"]
        eof = bool(request.get("eof"))
        digest = hashlib.sha256(data + bytes([eof])).hexdigest()
        async with self.input_lock:
            previous = self.receipts.get(offset)
            if previous is not None:
                if previous != (digest, len(data)):
                    raise ValueError("conflicting input replay")
                return self.info()
            if offset != self.input_offset or self.input_eof:
                raise ValueError("input offset does not match receipt")
            if self.state != "running" or self.writer is None:
                raise ValueError("resource input is closed")
            # The task remains alive if the bridge disappears during drain.
            self.writer.write(data)
            await self.writer.drain()
            if eof:
                if self.writer.can_write_eof():
                    self.writer.write_eof()
                else:
                    self.writer.close()
            self.input_offset += len(data)
            self.input_eof = eof
            self.receipts[offset] = (digest, len(data))
            # Only the latest receipt is needed: a client serializes writes.
            for old in list(self.receipts):
                if old != offset:
                    del self.receipts[old]
            return self.info()

    async def read(self, request):
        stream, offset = request.get("stream", "stdout"), request.get("offset", 0)
        if stream not in self.lengths or not isinstance(offset, int) or offset < 0:
            raise ValueError("invalid output cursor")
        if offset > self.lengths[stream]:
            raise ValueError("output cursor is beyond available output")
        if offset == self.lengths[stream] and self.state in ("admitted", "running"):
            self.changed.clear()
            # Recheck after clear to avoid a lost notification.
            if offset == self.lengths[stream] and self.state in ("admitted", "running"):
                with contextlib.suppress(asyncio.TimeoutError):
                    await asyncio.wait_for(self.changed.wait(), 20)
        data = b""
        if offset < self.lengths[stream]:
            with open(self.daemon.storage / (self.id + "." + stream), "rb") as source:
                source.seek(offset)
                data = source.read(CHUNK)
        return {**self.info(), "data": base64.b64encode(data).decode(), "offset": offset + len(data),
                "eof": self.state not in ("admitted", "running") and offset + len(data) == self.lengths[stream]}


class Daemon:
    def __init__(self, directory, token, lease_seconds=LEASE_SECONDS, storage=None):
        self.directory, self.token = Path(directory), token
        self.storage = Path(storage) if storage is not None else self.directory
        self.generation = uuid.uuid4().hex
        self.last_contact, self.lease_seconds = time.monotonic(), lease_seconds
        self.resources, self.tasks = {}, set()
        self.stopping = asyncio.Event()
        self.output_bytes = 0
        self.touch()

    def touch(self):
        self.last_contact = time.monotonic()
        (self.directory / "lease").touch(mode=0o600)

    async def dispatch(self, request):
        operation = request["op"]
        if operation in ("hello", "ping"):
            return {"version": VERSION, "generation": self.generation, "leaseSeconds": self.lease_seconds}
        if operation == "shutdown":
            self.stopping.set()
            return {"stopping": True}
        if request.get("generation") != self.generation:
            raise ValueError("daemon generation changed; previous work must not be replayed")
        identity = request.get("resource", "")
        if not IDENTIFIER.fullmatch(identity):
            raise ValueError("invalid resource ID")
        resource = self.resources.get(identity)
        if operation == "create":
            fingerprint = hashlib.sha256(json.dumps(
                {key: request.get(key) for key in ("kind", "command", "merge", "network", "address", "host", "port", "timeout", "idleTimeout")},
                sort_keys=True, separators=(",", ":")).encode()).hexdigest()
            if resource is not None and resource.fingerprint != fingerprint:
                raise ValueError("resource ID already belongs to another operation")
            if resource is None:
                receipt = identity + ".json"
                if (self.storage / receipt).exists() or (
                    self.storage != self.directory and (self.directory / receipt).exists()
                ):
                    raise ValueError("resource belonged to a previous daemon; execution state is unknown")
                if sum(item.state in ("admitted", "running") for item in self.resources.values()) >= RESOURCE_LIMIT:
                    raise ValueError("daemon resource limit reached")
                resource = Resource(self, identity, fingerprint, request["kind"])
                # Admission is durable before any side effect or await.
                try:
                    resource.journal()
                except OSError as error:
                    raise RuntimeError(
                        f"could not journal a new operation in {self.storage}: {error}"
                    ) from error
                self.resources[identity] = resource
            return await resource.start(request)
        if resource is None:
            raise ValueError("resource is unknown; it was not restarted")
        resource.last_contact = time.monotonic()
        if operation == "status":
            return resource.info()
        if operation == "read":
            return await resource.read(request)
        if operation == "write":
            return await resource.write(request)
        if operation == "stop":
            await resource.stop()
            return resource.info()
        raise ValueError("unsupported operation")

    async def handle(self, reader, writer):
        authenticated = False
        write_lock = asyncio.Lock()
        pending = set()

        async def respond(request):
            try:
                result = await self.dispatch(request)
                response = {"id": request["id"], "result": result}
            except Exception as error:
                response = {"id": request.get("id"), "error": str(error)}
            frame = json.dumps(response, separators=(",", ":")).encode()
            async with write_lock:
                if not writer.is_closing():
                    with contextlib.suppress(ConnectionError, OSError):
                        writer.write(struct.pack("!I", len(frame)) + frame)
                        await writer.drain()

        try:
            while True:
                size, = struct.unpack("!I", await reader.readexactly(4))
                if size > FRAME_LIMIT:
                    raise ValueError("frame too large")
                request = json.loads(await reader.readexactly(size))
                if not authenticated:
                    if request.get("op") != "hello" or not hmac.compare_digest(request.get("token", ""), self.token):
                        raise ValueError("authentication failed")
                    if request.get("version", VERSION) != VERSION:
                        frame = json.dumps({"id": request.get("id"), "error": "incompatible daemon protocol; detach the previous service to let its 15 minute lease expire"}).encode()
                        writer.write(struct.pack("!I", len(frame)) + frame)
                        await writer.drain()
                        return
                    authenticated = True
                self.touch()
                if len(pending) >= 1024:
                    raise ValueError("too many pending requests")
                task = asyncio.create_task(respond(request))
                pending.add(task)
                self.tasks.add(task)
                task.add_done_callback(pending.discard)
                task.add_done_callback(self.tasks.discard)
        except (asyncio.IncompleteReadError, ConnectionError, ValueError, KeyError, TypeError, json.JSONDecodeError):
            pass
        finally:
            if authenticated:
                self.touch()
            # Never cancel admitted operations when a relay disconnects.
            writer.close()

    async def supervise(self):
        while not self.stopping.is_set() and time.monotonic() - self.last_contact < self.lease_seconds:
            with contextlib.suppress(asyncio.TimeoutError):
                await asyncio.wait_for(self.stopping.wait(), min(5, self.lease_seconds))
            # Completed resources remain available for reconnect and restart.
            for identity, resource in list(self.resources.items()):
                if resource.idle_timeout and resource.state == "running" and time.monotonic() - resource.last_contact > resource.idle_timeout:
                    asyncio.create_task(resource.stop())
                if resource.finished_at and time.monotonic() - resource.finished_at > LEASE_SECONDS:
                    for suffix in (".stdout", ".stderr"):
                        path = self.storage / (identity + suffix)
                        if path.exists():
                            self.output_bytes -= path.stat().st_size
                            path.unlink()
                    del self.resources[identity]
                    # The small admission tombstone is deliberately retained.
        await asyncio.gather(*(resource.stop() for resource in self.resources.values()), return_exceptions=True)


async def main(directory):
    import fcntl
    directory = Path(directory)
    lock = open(directory / "daemon.lock", "a")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    daemon = Daemon(directory, (directory / "token").read_text(), storage=runtime_storage(directory))
    socket = directory / "socket"
    socket.unlink(missing_ok=True)
    server = await asyncio.start_unix_server(daemon.handle, path=str(socket), limit=FRAME_LIMIT)
    os.chmod(socket, 0o600)
    try:
        async with server:
            await daemon.supervise()
    finally:
        socket.unlink(missing_ok=True)


if __name__ == "__main__":
    asyncio.run(main(sys.argv[1]))
