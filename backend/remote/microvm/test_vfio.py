"""VFIO validation never writes sysfs or assigns devices during preflight."""

import errno
import importlib.util
import os
from pathlib import Path
import resource
import struct
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

from test_runner import runner


spec = importlib.util.spec_from_file_location(
    "vfio", Path(__file__).with_name("vfio.py")
)
vfio = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vfio)


class VFIOTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.pci = self.root / "pci"
        self.pci.mkdir()
        self.dev = self.root / "dev"
        self.dev.mkdir()
        self.cache = self.root / "cache"
        self.cache.mkdir(mode=0o700)
        self.driver = self.root / "drivers/vfio-pci"
        self.driver.mkdir(parents=True)
        (self.dev / "vfio").touch()
        self.gpu, self.audio = "0000:01:00.0", "0000:01:00.1"
        self.add_device(self.gpu, "0x030000")
        self.add_device(self.audio, "0x040300")

        self.ioctl = patch.object(vfio.fcntl, "ioctl", side_effect=self.query)
        self.ioctl.start()
        self.addCleanup(self.ioctl.stop)
        self.limit = patch.object(
            vfio.resource,
            "getrlimit",
            return_value=(resource.RLIM_INFINITY, resource.RLIM_INFINITY),
        )
        self.limit.start()
        self.addCleanup(self.limit.stop)

    def add_device(self, address, device_class, group="7"):
        device = self.pci / address
        device.mkdir()
        (device / "class").write_text(device_class)
        (device / "driver").symlink_to(self.driver, target_is_directory=True)
        directory = self.root / "groups" / group
        (directory / "devices").mkdir(parents=True, exist_ok=True)
        (device / "iommu_group").symlink_to(directory, target_is_directory=True)
        (directory / "devices" / address).symlink_to(device, target_is_directory=True)
        (self.dev / group).touch()

    def query(self, descriptor, operation, argument, mutate=False):
        if operation == vfio.VFIO_GROUP_GET_STATUS:
            argument[:] = struct.pack("II", 8, vfio.VFIO_GROUP_FLAGS_VIABLE)
            return 0
        if operation == vfio.VFIO_GET_API_VERSION:
            return 0
        if operation == vfio.VFIO_CHECK_EXTENSION:
            return 1
        self.fail("preflight attempted a non-query VFIO ioctl")

    def inspect(self, addresses=None):
        return vfio.inspect_devices(
            addresses or [self.gpu, self.audio], self.pci, self.dev
        )

    def test_prepared_complete_gpu_group_is_accepted(self):
        devices = self.inspect()
        self.assertEqual(
            [entry["address"] for entry in devices], [self.gpu, self.audio]
        )
        self.assertEqual({entry["group"] for entry in devices}, {"7"})
        self.assertTrue(devices[0]["gpu"])
        self.assertFalse(devices[1]["gpu"])
        self.assertEqual((self.pci / self.gpu / "driver").resolve(), self.driver)

    def test_missing_iommu_and_native_driver_fail_without_rebinding(self):
        group = self.pci / self.gpu / "iommu_group"
        group.unlink()
        with self.assertRaisesRegex(RuntimeError, "no IOMMU group"):
            self.inspect()
        group.symlink_to(self.root / "groups/7", target_is_directory=True)
        native = self.root / "drivers/nvidia"
        native.mkdir()
        driver = self.pci / self.gpu / "driver"
        driver.unlink()
        driver.symlink_to(native, target_is_directory=True)
        with self.assertRaisesRegex(RuntimeError, "bound to nvidia"):
            self.inspect()
        self.assertEqual(driver.resolve(), native)

    def test_gpu_requires_all_companions_and_rejects_unrelated_devices(self):
        with self.assertRaisesRegex(RuntimeError, "all companion"):
            self.inspect([self.gpu])
        with self.assertRaisesRegex(RuntimeError, "display/3D"):
            self.inspect([self.audio])
        self.add_device("0000:02:00.0", "0x010802", "8")
        with self.assertRaisesRegex(RuntimeError, "same-slot companion"):
            self.inspect([self.gpu, self.audio, "0000:02:00.0"])

    def test_nonviable_or_inaccessible_groups_and_low_memlock_fail(self):
        with patch.object(vfio, "group_status", return_value=0):
            with self.assertRaisesRegex(RuntimeError, "not viable"):
                self.inspect()
        (self.dev / "7").unlink()
        with self.assertRaisesRegex(RuntimeError, "not accessible"):
            self.inspect()
        (self.dev / "7").touch()
        with patch.object(vfio.resource, "getrlimit", return_value=(65536, 65536)):
            with self.assertRaisesRegex(RuntimeError, "memlock"):
                self.inspect()

    def test_inspection_of_a_running_vm_does_not_reopen_its_exclusive_vfio_group(self):
        with patch.object(
            vfio, "group_status", side_effect=RuntimeError("group already open in QEMU")
        ) as probe:
            devices = vfio.inspect_devices(
                [self.gpu, self.audio], self.pci, self.dev, probe_groups=False
            )
            self.assertEqual(len(devices), 2)
            probe.assert_not_called()
            with self.assertRaisesRegex(RuntimeError, "already open"):
                self.inspect()

    def test_external_qemu_exclusive_open_has_a_clear_error(self):
        with patch.object(
            vfio.os, "open", side_effect=OSError(errno.EBUSY, "Device or resource busy")
        ):
            with self.assertRaisesRegex(RuntimeError, "already open in a running VM"):
                vfio.group_status("7", self.dev)

    def test_pci_addresses_cannot_inject_paths_options_or_nonexistent_functions(self):
        for value in [
            "../../etc/passwd",
            "0000:01:00.0,romfile=/tmp/evil",
            "0000:01:20.0",
            "0000:01:00.8",
        ]:
            with self.subTest(value=value), self.assertRaises(RuntimeError):
                vfio.validate_addresses([value])
        with self.assertRaisesRegex(RuntimeError, "duplicate"):
            vfio.validate_addresses([self.gpu, self.gpu])

    def test_gpu_reservation_is_exclusive_and_released_by_closing_owner(self):
        devices = [{"group": "7"}]
        with patch.object(vfio, "group_status", return_value=1):
            owner = vfio.reserve_groups(self.cache, devices, {"agentId": "a"})
            try:
                with self.assertRaisesRegex(RuntimeError, "reserved by another agent"):
                    vfio.reserve_groups(self.cache, devices, {"agentId": "b"})
            finally:
                for descriptor in owner:
                    os.close(descriptor)
            replacement = vfio.reserve_groups(self.cache, devices, {"agentId": "b"})
            for descriptor in replacement:
                os.close(descriptor)

    def test_failed_multi_gpu_reservation_releases_prior_groups(self):
        with patch.object(vfio, "group_status", return_value=1):
            owner = vfio.reserve_groups(self.cache, [{"group": "8"}], {"agentId": "a"})
            try:
                with self.assertRaises(RuntimeError):
                    vfio.reserve_groups(
                        self.cache, [{"group": "7"}, {"group": "8"}], {"agentId": "b"}
                    )
                remaining = vfio.reserve_groups(
                    self.cache, [{"group": "7"}], {"agentId": "c"}
                )
                for descriptor in remaining:
                    os.close(descriptor)
            finally:
                for descriptor in owner:
                    os.close(descriptor)

    def test_external_vm_or_symlinked_reservations_cannot_be_claimed(self):
        with patch.object(vfio, "group_status", return_value=3):
            with self.assertRaisesRegex(RuntimeError, "already assigned"):
                vfio.reserve_groups(self.cache, [{"group": "7"}], {})
        (self.cache / "gpu-leases/7.lock").unlink()
        (self.cache / "gpu-leases/7.lock").symlink_to(self.root / "outside")
        with self.assertRaises(OSError):
            vfio.reserve_groups(self.cache, [{"group": "7"}], {})
        self.assertFalse((self.root / "outside").exists())


class Q35ArgumentTests(unittest.TestCase):
    def test_general_preflight_can_run_while_qemu_owns_the_gpu(self):
        probe = Mock()
        configuration = {"vmType": "q35", "gpuDevices": ["0000:01:00.0"]}
        available = subprocess.CompletedProcess(
            [], 0, 'q35 PC Q35\nname "vfio-pci"\n', ""
        )
        with (
            patch.object(runner.platform, "system", return_value="Linux"),
            patch.object(runner.platform, "machine", return_value="x86_64"),
            patch.object(runner.os, "open", return_value=123),
            patch.object(runner.os, "close"),
            patch.object(runner.fcntl, "ioctl", return_value=12),
            patch.object(runner.shutil, "which", return_value="/usr/bin/qemu"),
            patch.object(runner.subprocess, "run", return_value=available),
            patch.object(
                runner, "compatible_host_kernel", return_value=Path("/boot/kernel")
            ),
            patch.object(
                runner, "vfio_helpers", return_value={"inspect_devices": probe}
            ),
        ):
            self.assertEqual(runner.prerequisites(configuration), Path("/boot/kernel"))
        probe.assert_called_once_with(configuration["gpuDevices"], probe_groups=False)

    def arguments(self, request):
        with patch.object(
            runner.shutil, "which", return_value="/usr/bin/qemu-system-x86_64"
        ):
            return runner.virtme_arguments(
                Path("/cache/runtime"),
                Path("/boot/kernel"),
                Path("/cache/control"),
                request,
            )

    def test_q35_uses_pci_transports_and_fixed_gpu_root_ports(self):
        arguments = self.arguments(
            {
                "vmType": "q35",
                "editorPort": 12345,
                "gpuDevices": ["0000:01:00.1", "0000:01:00.0"],
            }
        )
        self.assertEqual(arguments[arguments.index("--arch") + 1], "x86_64")
        for option in (
            "q35,accel=kvm",
            "-nodefaults",
            "-no-user-config",
            "virtio-blk-pci,drive=fedora",
            "virtio-net-pci,netdev=zentrale",
        ):
            self.assertIn(option, arguments)
        self.assertTrue(
            any(
                option.startswith("vfio-pci,host=0000:01:00.0,bus=zentrale-gpu-0")
                for option in arguments
            )
        )
        self.assertTrue(
            any(
                option.startswith("vfio-pci,host=0000:01:00.1,bus=zentrale-gpu-0")
                for option in arguments
            )
        )
        self.assertEqual(
            sum(option.startswith("pcie-root-port,") for option in arguments), 1
        )
        functions = [option for option in arguments if option.startswith("vfio-pci,")]
        self.assertIn("host=0000:01:00.0,", functions[0])
        self.assertIn("multifunction=on", functions[0])

    def test_base_image_provisioning_never_claims_a_gpu(self):
        arguments = self.arguments(
            {"vmType": "q35", "gpuDevices": ["0000:01:00.0"], "provision": True}
        )
        self.assertFalse(any(option.startswith("vfio-pci,") for option in arguments))

    def test_gpu_requirements_cannot_be_passed_to_microvm_or_arbitrary_qemu_options(
        self,
    ):
        for request in [
            {"gpuDevices": ["0000:01:00.0"]},
            {"vmType": "q35", "gpuDevices": ["0000:01:00.0,romfile=evil"]},
        ]:
            with self.assertRaises(RuntimeError):
                self.arguments(request)

    def test_q35_probes_pci_instead_of_requiring_mmio_modules(self):
        import subprocess

        with (
            patch.object(runner.shutil, "which", return_value="modprobe"),
            patch.object(
                runner.subprocess,
                "run",
                return_value=subprocess.CompletedProcess([], 0, "", ""),
            ) as probe,
        ):
            runner.check_guest_kernel_modules(vm_type="q35")
        modules = [call.args[0][-1] for call in probe.call_args_list]
        self.assertIn("virtio_pci", modules)
        self.assertNotIn("virtio_mmio", modules)


if __name__ == "__main__":
    unittest.main()
