"""Trust boundaries and selection for the optional rootless GPU runtime."""

import importlib.util
import io
import json
import signal
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock, patch

from test_runner import runner

specification = importlib.util.spec_from_file_location(
    "zentrale_nvidia", Path(__file__).with_name("nvidia.py")
)
nvidia = importlib.util.module_from_spec(specification)
specification.loader.exec_module(nvidia)


class NVIDIAIntegrationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)

    def configuration(self, **options):
        return {
            "gpu": True,
            "gpuDevices": [],
            "vfio": "",
            "nvidia": "",
            **options,
        }

    def test_cpu_and_explicit_vfio_never_probe_native_nvidia(self):
        with patch.object(runner, "nvidia_helpers") as native:
            for configuration in (
                self.configuration(gpu=False),
                self.configuration(gpuDevices=["0000:01:00.0"]),
            ):
                self.assertIs(
                    runner.resolve_gpu_configuration(configuration), configuration
                )
            native.assert_not_called()

    def test_prepared_vfio_has_priority_over_driver_forwarding(self):
        with (
            patch.object(
                runner,
                "vfio_helpers",
                return_value={"discover_devices": lambda: ["0000:01:00.0"]},
            ),
            patch.object(runner, "nvidia_helpers") as native,
        ):
            selected = runner.resolve_gpu_configuration(self.configuration())
            self.assertEqual(selected["gpuDevices"], ["0000:01:00.0"])
            native.assert_not_called()

    def test_native_nvidia_is_selected_without_claiming_a_vfio_group(self):
        inventory = {
            "backend": "nvidia",
            "driver": "615.71.10",
            "devices": ["0000:01:00.0"],
        }
        with (
            patch.object(
                runner,
                "vfio_helpers",
                return_value={
                    "discover_devices": Mock(side_effect=RuntimeError("no VFIO"))
                },
            ),
            patch.object(
                runner, "nvidia_helpers", return_value={"discover": lambda: inventory}
            ),
        ):
            selected = runner.resolve_gpu_configuration(self.configuration())
        self.assertEqual(selected["gpuBackend"], "nvidia")
        self.assertEqual(selected["gpuDevices"], [])
        self.assertEqual(selected["gpuInventory"], inventory)

    def test_missing_native_device_fails_instead_of_starting_a_cpu_vm(self):
        with (
            patch.object(
                runner,
                "vfio_helpers",
                return_value={
                    "discover_devices": Mock(side_effect=RuntimeError("no VFIO"))
                },
            ),
            patch.object(
                runner,
                "nvidia_helpers",
                return_value={
                    "discover": Mock(side_effect=PermissionError("nvidia-uvm denied"))
                },
            ),
            self.assertRaisesRegex(RuntimeError, "no VFIO.*nvidia-uvm denied"),
        ):
            runner.resolve_gpu_configuration(self.configuration())

    def test_boot_gpu_can_be_forwarded_without_detaching_its_host_driver(self):
        pci = self.root / "pci"
        gpu = pci / "0000:01:00.0"
        gpu.mkdir(parents=True)
        (gpu / "class").write_text("0x030000")
        (gpu / "vendor").write_text("0x10de")
        (gpu / "boot_vga").write_text("1")
        driver = self.root / "drivers/nvidia"
        driver.mkdir(parents=True)
        (gpu / "driver").symlink_to(driver)
        version = self.root / "version"
        version.write_text("615.71.10")
        devices = self.root / "devices"
        devices.mkdir()
        for name in ("nvidia0", "nvidiactl", "nvidia-uvm"):
            (devices / name).touch()
        self.assertEqual(nvidia.discover(pci, devices, version)["devices"], [gpu.name])
        self.assertEqual((gpu / "driver").resolve(), driver)
        (devices / "nvidia-uvm").unlink()
        with self.assertRaises(FileNotFoundError):
            nvidia.discover(pci, devices, version)

    def archive(self, entries):
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode="w:gz") as archive:
            for name, data in entries:
                member = tarfile.TarInfo(name)
                if isinstance(data, bytes):
                    member.size = len(data)
                    archive.addfile(member, io.BytesIO(data))
                else:
                    member.type = tarfile.SYMTYPE
                    member.linkname = data
                    archive.addfile(member)
        return output.getvalue()

    def test_archive_links_never_redirect_extraction_outside_private_cache(self):
        archive = self.archive([("directory", "/tmp"), ("directory/owned", b"safe")])
        nvidia.extract_regular(archive, self.root)
        self.assertFalse((self.root / "directory").is_symlink())
        self.assertEqual((self.root / "directory/owned").read_bytes(), b"safe")
        for name in ("../escape", "/absolute"):
            with (
                self.subTest(name=name),
                self.assertRaisesRegex(RuntimeError, "unsafe"),
            ):
                nvidia.extract_regular(self.archive([(name, b"bad")]), self.root)

    def test_libraries_must_match_the_running_host_driver(self):
        library = self.root / "libcuda.so.580.1"
        library.touch()
        listing = subprocess.CompletedProcess(
            [], 0, "libcuda.so.1 (libc6,x86-64) => " + str(library), ""
        )
        with (
            patch.object(nvidia.subprocess, "run", return_value=listing),
            self.assertRaisesRegex(RuntimeError, "userspace is incomplete"),
        ):
            nvidia.libraries("615.71.10")

    def test_host_kernel_reuse_checks_graphics_without_loading_host_modules(self):
        with (
            patch.object(nvidia.Path, "is_file", return_value=True),
            patch.object(
                nvidia.subprocess,
                "run",
                return_value=subprocess.CompletedProcess([], 0),
            ) as probe,
        ):
            self.assertIsNotNone(nvidia.host_headers(Path("/boot/vmlinuz-host")))
            for call in probe.call_args_list:
                self.assertIn("--dry-run", call.args[0])
                self.assertIn("--config", call.args[0])
                self.assertIn("/dev/null", call.args[0])
        with (
            patch.object(nvidia.Path, "is_file", return_value=True),
            patch.object(
                nvidia.subprocess,
                "run",
                return_value=subprocess.CompletedProcess([], 1),
            ),
        ):
            self.assertIsNone(nvidia.host_headers(Path("/boot/vmlinuz-host")))

    def test_mismatched_fallback_headers_stop_before_any_download(self):
        release = {
            "kernel": {"version": "new"},
            "nvidia": {"headers": {"version": "old"}},
        }
        download, fallback = Mock(), Mock()
        with self.assertRaisesRegex(RuntimeError, "headers do not match"):
            nvidia.prepare(self.root, release, None, {}, download, fallback)
        download.assert_not_called()
        fallback.assert_not_called()

    def test_provisioning_never_attaches_a_gpu_or_exports_its_runtime(self):
        runtime = self.root / "runtime"
        request = self.configuration(
            provision=True,
            gpuBackend="nvidia",
            gpuRuntime="/private/gpu",
        )
        arguments = runner.virtme_arguments(
            runtime, Path("/boot/kernel"), self.root, request
        )
        self.assertNotIn("/private/gpu/qemu/bin/qemu-system-x86_64", arguments)
        self.assertFalse(
            any(
                "virtio-nvgpu" in value or "zentrale-nvidia=" in value
                for value in arguments
                if isinstance(value, str)
            )
        )
        request["provision"] = False
        arguments = runner.virtme_arguments(
            runtime, Path("/boot/kernel"), self.root, request
        )
        self.assertIn("/private/gpu/qemu/bin/qemu-system-x86_64", arguments)
        index = arguments.index("/run/zentrale-nvidia=/private/gpu/guest")
        self.assertEqual(arguments[index - 1], "--rodir")
        self.assertFalse(any("vfio-pci" in value for value in arguments))

    def test_driver_change_requires_a_new_session(self):
        previous = self.configuration(
            gpuBackend="nvidia", gpuInventory={"driver": "615.71.10"}
        )
        (self.root / "request.json").write_text(json.dumps(previous))
        self.assertTrue(runner.compatible_session(self.root, previous, {}))
        changed = dict(previous, gpuInventory={"driver": "615.72.0"})
        self.assertFalse(runner.compatible_session(self.root, changed, {}))

    def test_readiness_rejects_visible_but_unusable_cuda_device(self):
        failure = subprocess.CompletedProcess([], 1, "", "cuInit: 803")
        with (
            patch.object(nvidia.subprocess, "run", return_value=failure),
            self.assertRaisesRegex(RuntimeError, "readiness failed.*cuInit: 803"),
        ):
            nvidia.verify_guest(self.root)

    def test_gpu_helpers_are_reaped_when_virtme_exits_first(self):
        with (
            patch.object(runner.os, "waitpid", return_value=(1234, 0)),
            patch.object(runner.os, "killpg") as terminate,
        ):
            runner.terminate_vm(1234, self.root)
        terminate.assert_called_once_with(1234, signal.SIGKILL)


if __name__ == "__main__":
    unittest.main()
