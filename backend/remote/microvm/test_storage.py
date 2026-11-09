"""Project storage exports never include another project or the VM cache."""

from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from test_runner import runner
from test_kernel import guest


class StorageTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.source = self.root / "project-proof"
        self.source.mkdir()
        self.mount = {"id": "large", "hostPath": str(self.source), "guestPath": "/mnt/zentrale-storage/large"}
        self.execution = {"projectId": "proof", "workspace": str(self.root / "workspace"), "temporaryDirectory": str(self.root / "temporary"), "storage": [self.mount]}

    def test_only_prepared_project_directories_match_configured_volumes(self):
        self.assertEqual(runner.storage_mounts(self.execution, [{"id": "large"}]), [self.mount])
        for replacement in [dict(self.mount, guestPath="/etc"), dict(self.mount, hostPath=str(self.root)), dict(self.mount, id="../escape")]:
            with self.subTest(replacement=replacement), self.assertRaises(RuntimeError):
                runner.storage_mounts(dict(self.execution, storage=[replacement]))
        with self.assertRaisesRegex(RuntimeError, "not been prepared"):
            runner.storage_mounts(self.execution, [{"id": "other"}])
        self.source.rmdir()
        self.source.symlink_to(self.root, target_is_directory=True)
        with self.assertRaisesRegex(RuntimeError, "managed project directory"):
            runner.storage_mounts(self.execution)

    def test_qemu_exports_only_the_managed_volume_source(self):
        runtime = self.root / "runtime"
        runtime.mkdir()
        (runtime / "busybox").write_bytes(b"test")
        control = self.root / "control"
        configuration = {key: "test" for key in ("guest", "streams", "terminal", "init")}
        runner.prepare_control(control, configuration, runtime, self.execution)
        self.assertEqual((control / "storage-0").resolve(), self.source)
        with patch.object(runner.shutil, "which", return_value="/usr/bin/qemu"):
            args = runner.virtme_arguments(runtime, Path("/boot/kernel"), control, self.execution)
        export = "/run/zentrale-storage-0=" + str(control / "storage-0")
        self.assertEqual(args[args.index(export)-1], "--rwdir")
        self.assertNotIn("/mnt/zentrale-storage/large=" + str(self.root), args)

    def test_volume_exports_do_not_change_readonly_kernel_mount_semantics(self):
        cmdline = " ".join([
            "virtme_initmount0=/run/zentrale-workspace", "virtme_initmount1=/run/zentrale-temporary",
            "virtme_initmount2=/run/zentrale-control", "virtme_initmount3=/run/zentrale-storage-0",
            "virtme_initmount4=/run/zentrale-kernel",
        ])
        with patch.object(guest, "mount") as mount, patch.object(guest.os, "makedirs"), patch.object(guest.Path, "read_text", return_value=cmdline):
            guest.initialize_filesystems()
        exports = {call.args[1]: call.args for call in mount.call_args_list if call.args[2:3] == ("9p",)}
        self.assertEqual(exports["/run/zentrale-storage-0"][0], "virtme.initmount3")
        self.assertFalse(exports["/run/zentrale-storage-0"][3] & guest.READONLY)
        self.assertEqual(exports["/run/zentrale-kernel"][0], "virtme.initmount4")
        self.assertTrue(exports["/run/zentrale-kernel"][3] & guest.READONLY)

    def test_storage_cannot_export_the_protected_cache(self):
        with patch.object(runner, "cache_directory", side_effect=lambda roots: roots):
            roots = runner.validate_execution(self.execution)
        self.assertIn(self.source, roots)
        cache = self.root / "cache"
        with patch.object(runner, "private_directory", side_effect=lambda value: value), patch.object(runner.os, "getuid", return_value=123):
            with self.assertRaisesRegex(RuntimeError, "protected MicroVM cache"):
                runner.cache_directory([Path("/var/tmp")])


if __name__ == "__main__":
    unittest.main()
