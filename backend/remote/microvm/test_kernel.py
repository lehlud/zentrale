"""Kernel fallback is conditional, verified, private and guest-only."""

import importlib.util
import gzip
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

from test_runner import runner


specification = importlib.util.spec_from_file_location(
    "guest_init", Path(__file__).with_name("init.py")
)
guest = importlib.util.module_from_spec(specification)
specification.loader.exec_module(guest)


class GuestKernelTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.cache = runner.private_directory(Path(self.temporary.name) / "cache")
        self.artifact = {
            "version": "6.12.1-virt",
            "member": "boot/vmlinuz-virt",
            "config": "boot/config-virt",
            "sha256": "a" * 64,
        }

    def package(self, extra=None):
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode="w:gz") as archive:
            for name, data in {
                "boot/vmlinuz-virt": b"kernel image",
                "boot/config-virt": b"CONFIG_NAMESPACES=y\n",
                "lib/modules/6.12.1-virt/modules.dep": b"",
                "lib/modules/6.12.1-virt/kernel/fs/9p.ko": b"module",
            }.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
            if extra:
                archive.addfile(extra)
        return output.getvalue()

    def test_compatible_host_avoids_download_even_if_fallback_was_cached(self):
        (self.cache / "kernel-preexisting").mkdir()
        with (
            patch.object(runner.Path, "is_file", return_value=True),
            patch.object(runner.os, "access", return_value=True),
            patch.object(runner.Path, "read_text", return_value="CONFIG_NAMESPACES=y"),
            patch.object(runner, "check_guest_kernel_modules"),
        ):
            kernel = runner.compatible_host_kernel()
        self.assertEqual(kernel, Path("/boot/vmlinuz-" + runner.platform.release()))
        with (
            patch.object(runner, "install_kernel") as fallback,
            patch.object(runner, "install_runtime", return_value="runtime"),
            patch.object(runner, "provision_base", return_value="base"),
        ):
            self.assertEqual(
                runner.ensure_ready(self.cache, {"release": {}}, kernel),
                ("runtime", "base", kernel),
            )
            fallback.assert_not_called()

    def test_unreadable_optional_config_does_not_force_a_kernel_download(self):
        with (
            patch.object(runner.Path, "is_file", return_value=True),
            patch.object(runner.os, "access", return_value=True),
            patch.object(runner.Path, "read_text", side_effect=PermissionError()),
            patch.object(runner, "check_guest_kernel_modules"),
        ):
            self.assertIsNotNone(runner.compatible_host_kernel())

    def test_missing_drivers_or_namespaces_select_the_fallback(self):
        with (
            patch.object(runner.Path, "is_file", return_value=True),
            patch.object(runner.os, "access", return_value=True),
            patch.object(runner.Path, "read_text", return_value=""),
            patch.object(
                runner,
                "check_guest_kernel_modules",
                side_effect=RuntimeError("9p missing"),
            ),
        ):
            self.assertIsNone(runner.compatible_host_kernel())
        with (
            patch.object(runner.Path, "is_file", return_value=True),
            patch.object(runner.os, "access", return_value=True),
            patch.object(
                runner.Path, "read_text", return_value="# CONFIG_NET_NS is not set\n"
            ),
        ):
            self.assertIsNone(runner.compatible_host_kernel())
        with patch.object(runner.Path, "is_file", return_value=False):
            self.assertIsNone(runner.compatible_host_kernel())

    def test_fallback_failure_never_downgrades_isolation(self):
        with (
            patch.object(runner, "install_kernel", side_effect=RuntimeError("SHA-256")),
            patch.object(runner, "install_runtime") as runtime,
        ):
            with self.assertRaisesRegex(RuntimeError, "SHA-256") as failure:
                runner.ensure_ready(
                    self.cache, {"release": {"kernel": self.artifact}}, None
                )
            self.assertNotIsInstance(failure.exception, runner.UnavailableError)
            runtime.assert_not_called()

    def test_kernel_and_matching_modules_are_installed_atomically_and_reused(self):
        with (
            patch.object(runner, "download", return_value=self.package()) as download,
            patch.object(runner, "check_guest_kernel_modules") as modules,
        ):
            kernel = runner.install_kernel(self.cache, self.artifact)
            self.assertEqual(runner.install_kernel(self.cache, self.artifact), kernel)
            download.assert_called_once_with(self.artifact, limit=64 * 1024 * 1024)
            modules.assert_called_once()
            self.assertEqual(modules.call_args.args[1], self.artifact["version"])

        root = kernel.parent.parent
        self.assertEqual(kernel.read_bytes(), b"kernel image")
        self.assertEqual(
            (root / "lib/modules/6.12.1-virt/kernel/fs/9p.ko").read_bytes(), b"module"
        )
        self.assertEqual((root / "lib").resolve(), root / "usr/lib")
        self.assertEqual(
            json.loads((root / "installed.json").read_text())["artifact"], self.artifact
        )

    def test_package_cannot_escape_or_install_links_or_devices(self):
        for name, kind in [
            ("../../outside", tarfile.REGTYPE),
            ("/etc/passwd", tarfile.REGTYPE),
            ("lib/modules/6.12.1-virt/link", tarfile.SYMTYPE),
            ("lib/modules/6.12.1-virt/hardlink", tarfile.LNKTYPE),
            ("lib/modules/6.12.1-virt/device", tarfile.CHRTYPE),
        ]:
            member = tarfile.TarInfo(name)
            member.type, member.linkname = kind, "/etc/passwd"
            with (
                self.subTest(name=name),
                patch.object(runner, "download", return_value=self.package(member)),
            ):
                with self.assertRaisesRegex(RuntimeError, "unsafe"):
                    runner.install_kernel(self.cache, self.artifact)
            self.assertEqual(list(self.cache.iterdir()), [])

    def test_compressed_modules_are_unpacked_and_dependency_metadata_regenerated(self):
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w:gz") as package:
            for name, data in {
                "boot/vmlinuz-virt": b"kernel",
                "lib/modules/6.12.1-virt/kernel/virtio_blk.ko.gz": gzip.compress(
                    b"module"
                ),
            }.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                package.addfile(member, io.BytesIO(data))
        with (
            patch.object(runner, "download", return_value=archive.getvalue()),
            patch.object(runner.shutil, "which", return_value="/usr/sbin/depmod"),
            patch.object(runner.subprocess, "run") as depmod,
            patch.object(runner, "check_guest_kernel_modules"),
        ):
            kernel = runner.install_kernel(self.cache, self.artifact)
        module = kernel.parent.parent / "lib/modules/6.12.1-virt/kernel/virtio_blk.ko"
        self.assertEqual(module.read_bytes(), b"module")
        self.assertFalse(module.with_suffix(".ko.gz").exists())
        self.assertEqual(depmod.call_args.args[0][-1], "6.12.1-virt")
        self.assertIn("--basedir", depmod.call_args.args[0])

    def test_checksum_and_expansion_limits_are_enforced_before_installation(self):
        with patch.object(
            runner.urllib.request, "urlopen", return_value=io.BytesIO(self.package())
        ):
            with self.assertRaisesRegex(RuntimeError, "SHA-256"):
                runner.install_kernel(
                    self.cache, dict(self.artifact, url="https://example.test/kernel")
                )
        with (
            patch.object(runner, "download", return_value=self.package()),
            patch.object(runner, "MAX_KERNEL_EXTRACT_BYTES", 5),
        ):
            with self.assertRaisesRegex(RuntimeError, "extraction limit"):
                runner.install_kernel(self.cache, self.artifact)
        self.assertEqual(list(self.cache.iterdir()), [])

    def test_fallback_export_is_readonly_and_guest_modprobe_uses_its_modules(self):
        kernel = self.cache / "kernel/boot/vmlinuz-6.12.1-virt"
        with patch.object(
            runner.shutil, "which", return_value="/usr/bin/qemu-system-x86_64"
        ):
            arguments = runner.virtme_arguments(
                self.cache / "runtime", kernel, self.cache / "control", {}
            )
            export = "/run/zentrale-kernel=" + str(kernel.parent.parent)
            self.assertEqual(arguments[arguments.index(export) - 1], "--rodir")
            host_arguments = runner.virtme_arguments(
                self.cache / "runtime",
                Path("/boot/vmlinuz-host"),
                self.cache / "control",
                {},
            )
            self.assertNotIn("--rodir", host_arguments)
        with (
            patch.object(guest, "mount") as mount,
            patch.object(guest.os, "makedirs"),
            patch.object(
                guest.Path,
                "read_text",
                return_value="virtme_initmount0=/run/zentrale-workspace virtme_initmount1=/run/zentrale-temporary virtme_initmount2=/run/zentrale-control virtme_initmount3=/run/zentrale-kernel",
            ),
        ):
            guest.initialize_filesystems()
            args = mount.call_args.args
            self.assertEqual(
                args[0:3], ("virtme.initmount3", "/run/zentrale-kernel", "9p")
            )
            self.assertTrue(args[3] & guest.READONLY)
        with (
            patch.object(guest.Path, "is_dir", return_value=True),
            patch.object(guest.subprocess, "run") as probe,
        ):
            probe.return_value.returncode = 0
            guest.load_module("virtio_blk")
            arguments = probe.call_args.args[0]
            self.assertEqual(
                arguments[arguments.index("--dirname") + 1], "/run/zentrale-kernel"
            )
            self.assertIn("/dev/null", arguments)


if __name__ == "__main__":
    unittest.main()
