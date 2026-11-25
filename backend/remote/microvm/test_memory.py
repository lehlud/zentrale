import tempfile
import unittest
from pathlib import Path

from memory import DEFAULT_RAM, MIB, allocation, memory_budget


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
        self.assertEqual(allocation(capacity, 18 * GIB, 2 * GIB, 0, [], DEFAULT_RAM, True), 20 * GIB)
        self.assertEqual(allocation(capacity, 6 * GIB, 3 * GIB, 0, [], 20 * GIB, True), 9 * GIB)

    def test_growth_waits_for_another_guests_balloon_request_to_take_effect(self):
        # The peer's outstanding 8 GiB cannot be given away a second time.
        others = [(9 * GIB, GIB)]
        self.assertEqual(allocation(30 * GIB, 8 * GIB, 2 * GIB, 0, others, 4 * GIB, True), 4 * GIB)
        self.assertIsNone(allocation(30 * GIB, 8 * GIB, GIB, 0, others, 30 * GIB, False))
        others = [(4 * GIB, GIB)]
        self.assertEqual(allocation(30 * GIB, 8 * GIB, GIB, 0, others, 30 * GIB, False), 5 * GIB)

    def test_host_pressure_preserves_the_guests_swapped_working_set(self):
        self.assertEqual(allocation(30 * GIB, GIB, 5 * GIB, 3 * GIB, [], 10 * GIB, True), 8 * GIB)
