import tempfile
import unittest
from contextlib import contextmanager
from pathlib import Path
from unittest.mock import patch

import memory
from memory import DEFAULT_RAM, MIB, MemoryController, allocation, memory_budget


GIB = 1024 * MIB


class ManagedMemoryTests(unittest.TestCase):
    def test_physical_budget_respects_ancestor_cgroups_and_ignores_swap(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            proc, cgroups = root / "proc", root / "cgroups"
            (proc / "self").mkdir(parents=True)
            (proc / "meminfo").write_text(
                "MemTotal: 33554432 kB\nMemAvailable: 25165824 kB\n"
                "SwapTotal: 8388608 kB\nSwapFree: 8388608 kB\n"
            )
            (proc / "self/cgroup").write_text("0::/user.slice/session\n")
            (cgroups / "user.slice/session").mkdir(parents=True)
            (cgroups / "user.slice/session/memory.max").write_text("max")
            (cgroups / "user.slice/memory.max").write_text(str(16 * GIB))
            (cgroups / "user.slice/memory.current").write_text(str(8 * GIB))
            self.assertEqual(memory_budget(proc, cgroups), (15 * GIB, 7 * GIB))
            (cgroups / "user.slice/memory.max").write_text("max")
            self.assertEqual(memory_budget(proc, cgroups), (30 * GIB, 22 * GIB))

    def test_single_vm_can_borrow_host_headroom_and_releases_it_under_pressure(self):
        capacity = 30 * GIB
        self.assertEqual(allocation(capacity, 18 * GIB, 2 * GIB, 0, [], DEFAULT_RAM, True), (20 * GIB, 20 * GIB))
        self.assertEqual(allocation(capacity, 6 * GIB, 3 * GIB, 0, [], 20 * GIB, True), (9 * GIB, 9 * GIB))

    def test_growth_waits_for_another_guests_balloon_request_to_take_effect(self):
        # The peer's outstanding 8 GiB cannot be given away a second time.
        others = [(9 * GIB, GIB)]
        self.assertEqual(allocation(30 * GIB, 8 * GIB, 2 * GIB, 0, others, 4 * GIB, True), (4 * GIB, 2 * GIB))
        self.assertEqual(allocation(30 * GIB, 8 * GIB, GIB, 0, others, 30 * GIB, False), (4 * GIB, GIB))
        others = [(4 * GIB, GIB)]
        self.assertEqual(allocation(30 * GIB, 8 * GIB, GIB, 0, others, 30 * GIB, False), (5 * GIB, 6 * GIB))

    def test_host_pressure_preserves_the_guests_swapped_working_set(self):
        target, _ = allocation(30 * GIB, GIB, 5 * GIB, 3 * GIB, [], 10 * GIB, True)
        self.assertEqual(target, 8 * GIB)

    def test_slow_initial_balloon_is_admitted_after_sixty_seconds(self):
        actual = 30 * GIB

        @contextmanager
        def monitor(_):
            def execute(command, arguments=None):
                if command == "query-balloon":
                    return {"actual": actual}
                self.assertEqual(command, "balloon")
                self.assertEqual(arguments, {"value": 20 * GIB})
            yield execute

        with (
            tempfile.TemporaryDirectory() as directory,
            patch.object(memory, "monitor", monitor),
            patch.object(memory, "process_memory", return_value=("1", 2 * GIB, 0)),
            patch.object(memory, "memory_budget", return_value=(30 * GIB, 18 * GIB)),
            patch.object(memory.time, "monotonic", return_value=0) as clock,
        ):
            control = Path(directory)
            controller = MemoryController(control, control, "test", 30 * GIB, 1)
            (control / "qemu.pid").write_text("42")
            # Before the guest reaches allocation, the kernel boot has its own deadline.
            self.assertIsNone(controller.admission_failure())
            (control / "startup-phase").write_text("vm_allocating_memory")
            controller.sample()
            self.assertIsNone(controller.admission_failure())

            clock.return_value = 45
            actual = 26 * GIB
            controller.sample()
            clock.return_value = 75
            self.assertIsNone(controller.admission_failure())
            self.assertFalse((control / "memory-ready").exists())

            # Repeated QMP replies with the same capacity are not progress.
            clock.return_value = 105
            controller.sample()
            failure = controller.admission_failure()
            self.assertEqual(failure["reason"], "memory_admission_timeout")
            self.assertIn("guest capacity 26624.0 MiB, target 20480.0 MiB", failure["error"])

            clock.return_value = 107
            actual = 20 * GIB
            controller.sample()
            self.assertTrue((control / "memory-ready").exists())
            self.assertIsNone(controller.admission_failure())

    def test_waiting_guests_prepare_while_a_peer_releases_its_reservation(self):
        actual = {"first": 30 * GIB, "second": 30 * GIB}
        requests = {}

        @contextmanager
        def monitor(path):
            def execute(command, arguments=None):
                if command == "query-balloon":
                    return {"actual": actual[path.stem]}
                self.assertEqual(command, "balloon")
                requests[path.stem] = arguments["value"]
            yield execute

        with (
            tempfile.TemporaryDirectory() as directory,
            patch.object(memory, "monitor", monitor),
            patch.object(memory, "process_memory", return_value=("1", GIB, 0)),
            patch.object(memory, "memory_budget", return_value=(30 * GIB, 9 * GIB)),
            patch.object(memory.time, "monotonic", return_value=0) as clock,
        ):
            root = Path(directory)
            peer = MemoryController(root, root, "peer", 30 * GIB, 1)
            peer.publish(10, "1", 12 * GIB)
            guests = []
            for pid, name in enumerate(actual, 20):
                control = root / name
                control.mkdir()
                (control / "qemu.pid").write_text(str(pid))
                (control / "startup-phase").write_text("vm_allocating_memory")
                guests.append(MemoryController(control, root, name, 30 * GIB, 1))
            first, second = guests

            for guest in guests:
                guest.sample()
                self.assertEqual(requests[guest.socket.stem], DEFAULT_RAM)
                self.assertEqual(guest.published["limit"], GIB)
                self.assertIsNone(guest.admission_failure())

            # Both finish their own reductions before the occupied host can
            # grant even one of them a full budget.
            actual = dict.fromkeys(actual, DEFAULT_RAM)
            for now, peer_limit in [(45, 12), (90, 10), (115, 10), (135, 8)]:
                clock.return_value = now
                peer.publish(10, "1", peer_limit * GIB)
                for guest in guests:
                    guest.sample()
                    self.assertFalse((guest.control / "memory-ready").exists())
                    self.assertIsNone(guest.admission_failure())

            clock.return_value = 180
            peer.publish(10, "1", 7 * GIB)
            first.sample()
            second.sample()
            self.assertTrue((first.control / "memory-ready").exists())
            self.assertEqual(first.published["limit"], DEFAULT_RAM)
            self.assertFalse((second.control / "memory-ready").exists())
            for now, peer_limit in [(210, 6), (240, 5), (270, 4)]:
                clock.return_value = now
                peer.publish(10, "1", peer_limit * GIB)
                second.sample()
                self.assertIsNone(second.admission_failure())
            self.assertTrue((second.control / "memory-ready").exists())

    def test_hotplug_admits_boot_ram_before_requesting_extra_memory(self):
        plugged = 0
        requests = []

        @contextmanager
        def monitor(_):
            def execute(command, arguments=None):
                if command == "query-memory-size-summary":
                    return {"base-memory": DEFAULT_RAM, "plugged-memory": plugged}
                self.assertEqual(command, "qom-set")
                self.assertEqual(arguments["property"], "requested-size")
                requests.append(arguments["value"])
            yield execute

        with (
            tempfile.TemporaryDirectory() as directory,
            patch.object(memory, "monitor", monitor),
            patch.object(memory, "process_memory", return_value=("1", GIB, 0)),
            patch.object(memory, "memory_budget", return_value=(16 * GIB, 3 * GIB - MIB)) as budget,
            patch.object(memory.time, "monotonic", return_value=0) as clock,
        ):
            control = Path(directory)
            (control / "qemu.pid").write_text("42")
            controller = MemoryController(control, control, "test", 16 * GIB, 1, hotplug=True)
            controller.sample()
            self.assertFalse((control / "memory-ready").exists())
            self.assertEqual(controller.published["limit"], GIB)

            clock.return_value = 2
            budget.return_value = (16 * GIB, 12 * GIB)
            controller.sample()
            self.assertTrue((control / "memory-ready").exists())
            self.assertEqual(controller.published["limit"], DEFAULT_RAM)
            self.assertEqual(requests, [])

            clock.return_value = 4
            controller.sample()
            self.assertEqual(requests, [9 * GIB])
            self.assertEqual(controller.published["limit"], 13 * GIB)

            clock.return_value = 6
            plugged = 9 * GIB
            budget.return_value = (16 * GIB, 5 * GIB)
            controller.sample()
            self.assertEqual(requests[-1], 2 * GIB)
            self.assertEqual(controller.published["limit"], 13 * GIB)

            clock.return_value = 8
            plugged = 2 * GIB
            controller.sample()
            self.assertEqual(controller.published["limit"], 6 * GIB)
