"""Share physical host RAM between managed guests using VirtIO."""

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
ADMISSION_STALL_TIMEOUT = 60
ADMISSION_BUDGET_STEP = 32 * MIB
HOTPLUG_BLOCK = 2 * MIB
HOTPLUG_DEVICE = "zentrale-mem"


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
    """Return the balloon target and the guest's budget after peer reservations."""
    # Each guest gets its backed RAM plus an equal share of spare host RAM.
    # Count other guests' allowances until QEMU confirms they have been reduced.
    share = available // (len(others) + 1)
    unbacked = sum(max(0, limit - rss) for limit, rss in others)
    desired = (resident + share) // STEP * STEP
    budget = resident + available - unbacked
    affordable = max(resident, budget)
    # Preserve backing that may currently be swapped out. Under contention,
    # shrink allowances without taking memory away from a live working set.
    floor = max(DEFAULT_RAM, min(current, resident + swapped))
    if not ready:
        floor = DEFAULT_RAM
    wanted = min(capacity, max(floor, desired))
    if wanted > current or not ready:
        wanted = min(wanted, max(current if ready else DEFAULT_RAM, affordable))
    return wanted // MIB * MIB, budget


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
    def __init__(self, control, directory, name, capacity, group, hotplug=False):
        self.control = control
        self.directory = directory
        self.socket = directory / (name + ".sock")
        self.record = directory / (name + ".json")
        self.capacity = capacity
        self.group = group
        self.hotplug = hotplug
        self.target = DEFAULT_RAM if hotplug else None
        self.published = None
        self.next_sample = 0
        self.lowest_actual = None
        self.last_progress = None
        self.affordable = None
        self.budget_checkpoint = None
        self.sample_error = None

    def publish(self, pid, started, limit):
        record = {"pid": pid, "start": started, "limit": limit}
        if record == self.published:
            return
        temporary = self.record.with_suffix(".new")
        temporary.write_text(json.dumps(record))
        os.replace(temporary, self.record)
        self.published = record

    def current_capacity(self, execute):
        if self.hotplug:
            size = execute("query-memory-size-summary")
            return size["base-memory"] + size.get("plugged-memory", 0)
        return execute("query-balloon")["actual"]

    def set_capacity(self, execute, target):
        if self.hotplug:
            execute("qom-set", {
                "path": "/machine/peripheral/" + HOTPLUG_DEVICE,
                "property": "requested-size",
                "value": target - DEFAULT_RAM,
            })
        else:
            execute("balloon", {"value": target})

    def sample(self):
        now = time.monotonic()
        if now < self.next_sample:
            return
        self.next_sample = now + 2
        pid = int((self.control / "qemu.pid").read_text())
        started, resident, swapped = process_memory(pid, group=self.group)
        ready = (self.control / "memory-ready").exists()
        with monitor(self.socket) as execute:
            actual = self.current_capacity(execute)
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
                target, budget = allocation(
                    self.capacity, available, resident, swapped, others, actual, ready
                )
                if self.hotplug:
                    # The guest starts with 4 GiB already in its memory map.
                    # Wait for that budget only; grow after making it usable.
                    target = target // HOTPLUG_BLOCK * HOTPLUG_BLOCK if ready else DEFAULT_RAM
                if not ready:
                    # A waiting guest can inflate its balloon in parallel with
                    # its peers. Its PID 1 cannot run workloads until admitted,
                    # so only the RAM already backed on the host is in use.
                    self.publish(pid, started, resident)
                    self.affordable = max(resident, budget)
                    self.observe_admission(actual, budget, now)
                if (
                    self.target is None
                    or abs(self.target - target) >= STEP
                    or (not ready and actual <= self.target and target < self.target)
                ):
                    if ready:
                        # Reserve growth before sending it: QEMU can accept a
                        # command even when its monitor connection times out.
                        reserved = self.published["limit"] if self.published else 0
                        self.publish(pid, started, max(actual, reserved, target))
                    # An ambiguous reply must be retried before the old
                    # reservation can be released.
                    self.target = None
                    self.set_capacity(execute, target)
                    self.target = target
                if ready:
                    self.publish(pid, started, max(actual, self.target))
                elif self.target <= self.affordable and actual <= self.target:
                    # Commit the RAM reservation under the same lock as the
                    # budget check, before releasing the guest to do work.
                    self.publish(pid, started, self.target)
                    (self.control / "memory-ready").touch()
                self.sample_error = None

    def observe_admission(self, actual, budget, now):
        balloon_advanced = self.lowest_actual is None or actual < self.lowest_actual
        if balloon_advanced:
            self.lowest_actual = actual
        # Keep a signed budget so peer progress is visible even before their
        # outstanding reservations leave any RAM available for this guest.
        if self.budget_checkpoint is None:
            self.budget_checkpoint = budget
        if balloon_advanced or budget >= self.budget_checkpoint + ADMISSION_BUDGET_STEP:
            self.budget_checkpoint = budget
            if self.last_progress is not None:
                self.last_progress = now
        elif budget <= self.budget_checkpoint - ADMISSION_BUDGET_STEP:
            # Another guest may claim some RAM while we wait. Measure further
            # releases against the new budget without crediting the decrease.
            self.budget_checkpoint = budget

    def admission_failure(self):
        if (self.control / "memory-ready").exists():
            return None
        now = time.monotonic()
        if self.last_progress is None:
            try:
                phase = (self.control / "startup-phase").read_text(encoding="ascii")
            except FileNotFoundError:
                return None
            if phase == "vm_allocating_memory":
                self.last_progress = now
            return None
        if now - self.last_progress < ADMISSION_STALL_TIMEOUT:
            return None
        message = f"MicroVM RAM allocation made no progress for {ADMISSION_STALL_TIMEOUT} seconds"
        if self.target is not None and self.lowest_actual is not None:
            message += f" (guest capacity {self.lowest_actual / MIB:.1f} MiB, target {self.target / MIB:.1f} MiB)"
        if self.affordable is not None and self.affordable < DEFAULT_RAM:
            message += (
                f" (safe budget {self.affordable // MIB} MiB after other VM reservations,"
                f" requires {DEFAULT_RAM // MIB} MiB)"
            )
        if self.sample_error is not None:
            message += ": " + self.sample_error
        return {"reason": "memory_admission_timeout", "error": message}

    def close(self):
        self.record.unlink(missing_ok=True)
        self.record.with_suffix(".new").unlink(missing_ok=True)
        self.socket.unlink(missing_ok=True)
