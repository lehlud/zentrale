"""Share physical host RAM between managed guests using the VirtIO balloon."""

from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import socket
import time


MIB = 1024 * 1024
DEFAULT_RAM = 4096 * MIB
STEP = 256 * MIB


def memory_budget(proc=Path("/proc"), cgroups=Path("/sys/fs/cgroup")):
    fields = {}
    for line in (proc / "meminfo").read_text().splitlines():
        name, _, value = line.partition(":")
        if name in ("MemTotal", "MemAvailable"):
            fields[name] = int(value.split()[0]) * 1024
    total, available = fields["MemTotal"], fields["MemAvailable"]
    for line in (proc / "self/cgroup").read_text().splitlines():
        _, controllers, group = line.split(":", 2)
        if not controllers:
            root, limits, usage = cgroups, ("memory.max", "memory.high"), "memory.current"
        elif "memory" in controllers.split(","):
            root, limits, usage = cgroups / "memory", ("memory.limit_in_bytes",), "memory.usage_in_bytes"
        else:
            continue
        group = root / group.lstrip("/")
        if ".." in group.parts:
            continue
        while True:
            for filename in limits:
                try:
                    limit = (group / filename).read_text().strip()
                    if limit == "max":
                        continue
                    limit = int(limit)
                    in_use = int((group / usage).read_text())
                    total = min(total, limit)
                    available = min(available, limit - in_use)
                except FileNotFoundError:
                    pass
            if group == root:
                break
            group = group.parent
    # Leave room for the host and QEMU's page tables and device allocations.
    # MemAvailable already accounts for backing used by zram; swap capacity
    # never enters either the ceiling or the usable headroom.
    reserve = max(512 * MIB, min(2048 * MIB, total // 16))
    return max(0, total - reserve), max(0, available - reserve)


def process_memory(pid, start=None, group=None):
    root = Path("/proc") / str(pid)
    fields = (root / "stat").read_text().rsplit(") ", 1)[1].split()
    started = fields[19]
    if fields[0] == "Z" or (start is not None and started != start):
        raise ProcessLookupError("VM memory owner has exited")
    if group is not None and int(fields[2]) != group:
        raise ProcessLookupError("QEMU is outside its supervisor's process group")
    resident, swapped = 0, 0
    for line in (root / "status").read_text().splitlines():
        if line.startswith("RssAnon:"):
            resident = int(line.split()[1]) * 1024
        elif line.startswith("VmSwap:"):
            swapped = int(line.split()[1]) * 1024
    return started, resident, swapped


def allocation(capacity, available, resident, swapped, others, current, ready):
    """Return an allocation, or None while a new guest waits for a 4 GiB budget."""
    # Each guest gets its backed RAM plus an equal share of spare host RAM.
    # Count other guests' allowances until QEMU confirms they have been reduced.
    share = available // (len(others) + 1)
    unbacked = sum(max(0, limit - rss) for limit, rss in others)
    desired = (resident + share) // STEP * STEP
    affordable = resident + max(0, available - unbacked)
    if not ready and affordable < DEFAULT_RAM:
        return None
    # Preserve backing that may currently be swapped out. Under contention,
    # shrink allowances without taking memory away from a live working set.
    floor = max(DEFAULT_RAM, min(current, resident + swapped))
    if not ready:
        floor = DEFAULT_RAM
    wanted = min(capacity, max(floor, desired))
    if wanted > current or not ready:
        wanted = min(wanted, max(current if ready else DEFAULT_RAM, affordable))
    return wanted // MIB * MIB


@contextmanager
def monitor(path):
    # QMP is only reachable from the host through the runner's private cache.
    # Each short connection is independent so a busy monitor cannot block
    # supervisor work or leave an incomplete reply in a reused connection.
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(0.2)
        connection.connect(str(path))
        with connection.makefile("rb") as stream:
            greeting = json.loads(stream.readline(65536))
            if "QMP" not in greeting:
                raise ValueError("invalid QEMU monitor greeting")

            def execute(command, arguments=None):
                request = {"execute": command}
                if arguments is not None:
                    request["arguments"] = arguments
                connection.sendall(json.dumps(request).encode() + b"\n")
                for _ in range(32):
                    response = json.loads(stream.readline(65536))
                    if "return" in response:
                        return response["return"]
                    if "error" in response:
                        raise ValueError(str(response["error"]))
                raise ValueError("missing QEMU monitor reply")

            execute("qmp_capabilities")
            yield execute


class MemoryController:
    def __init__(self, control, directory, name, capacity, group):
        self.control = control
        self.directory = directory
        self.socket = directory / (name + ".sock")
        self.record = directory / (name + ".json")
        self.capacity = capacity
        self.group = group
        self.target = None
        self.published = None
        self.next_sample = 0

    def publish(self, pid, started, limit):
        record = {"pid": pid, "start": started, "limit": limit}
        if record == self.published:
            return
        temporary = self.record.with_suffix(".new")
        temporary.write_text(json.dumps(record))
        os.replace(temporary, self.record)
        self.published = record

    def sample(self):
        now = time.monotonic()
        if now < self.next_sample:
            return
        self.next_sample = now + 2
        pid = int((self.control / "qemu.pid").read_text())
        started, resident, swapped = process_memory(pid, group=self.group)
        with monitor(self.socket) as execute:
            actual = execute("query-balloon")["actual"]
            with (self.directory / "lock").open("a") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                others = []
                for record in self.directory.glob("*.json"):
                    if record == self.record:
                        continue
                    try:
                        previous = json.loads(record.read_text())
                        _, rss, _ = process_memory(previous["pid"], previous["start"])
                    except (FileNotFoundError, ProcessLookupError):
                        record.unlink(missing_ok=True)
                        continue
                    others.append((previous["limit"], rss))
                _, available = memory_budget()
                ready = (self.control / "memory-ready").exists()
                target = allocation(
                    self.capacity, available, resident, swapped, others, actual, ready
                )
                if target is not None and (
                    self.target is None or abs(self.target - target) >= STEP
                ):
                    # A timeout can happen after QEMU accepted the command.
                    # Reserve an increase before sending it; wait for an
                    # acknowledged reduction before another guest uses it.
                    observed = actual if ready else resident
                    reserved = self.published["limit"] if self.published else 0
                    self.publish(pid, started, max(observed, reserved, target))
                    execute("balloon", {"value": target})
                    self.target = target
                if self.target is None:
                    # Announce the incoming guest so live siblings share spare
                    # capacity while it waits for its initial allocation.
                    limit = resident
                else:
                    limit = max(actual, self.target) if ready else self.target
                self.publish(pid, started, limit)
                if not ready and self.target is not None and actual <= self.target:
                    (self.control / "memory-ready").touch()

    def close(self):
        self.record.unlink(missing_ok=True)
        self.record.with_suffix(".new").unlink(missing_ok=True)
        self.socket.unlink(missing_ok=True)
