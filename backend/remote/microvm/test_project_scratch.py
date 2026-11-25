import errno
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import project_scratch as scratch


class ProjectScratchTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        self.source = root / "project"
        self.backing = root / "disk" / "project"
        for name, value in [
            ("managed_scratch_path", True),
            ("scratch_backing", self.backing),
            ("offload_available", True),
        ]:
            replacement = patch.object(scratch, name, return_value=value)
            replacement.start()
            self.addCleanup(replacement.stop)
        self.source.mkdir(mode=0o700)

    def test_migration_preserves_paths_and_hardlinks_and_reuses_disk_after_reboot(self):
        (self.source / "file").write_text("working")
        os.link(self.source / "file", self.source / "hardlink")
        (self.source / "relative").symlink_to("file")

        scratch.offload_project_scratch(self.source)
        self.assertEqual(self.source.readlink(), self.backing)
        self.assertEqual((self.source / "relative").read_text(), "working")
        self.assertEqual((self.source / "file").stat().st_ino,
                         (self.source / "hardlink").stat().st_ino)

        self.source.unlink()  # Host reboot clears the tmpfs alias.
        scratch.prepare_project_scratch(self.source)
        scratch.offload_project_scratch(self.source)
        self.assertEqual((self.source / "file").read_text(), "working")
        scratch.remove_project_scratch(self.source)
        self.assertFalse(self.source.is_symlink())
        self.assertFalse(self.backing.exists())

    def test_failed_copy_leaves_original_in_place_for_retry(self):
        (self.source / "file").write_text("working")
        with patch.object(scratch.subprocess, "run", side_effect=OSError(errno.EDQUOT, "quota")):
            with self.assertRaises(OSError):
                scratch.offload_project_scratch(self.source)
        self.assertFalse(self.source.is_symlink())
        self.assertEqual((self.source / "file").read_text(), "working")
        self.assertFalse(self.backing.exists())
        scratch.offload_project_scratch(self.source)
        self.assertEqual((self.source / "file").read_text(), "working")

    def test_retry_after_failed_switch_copies_changes_to_the_original(self):
        payload = self.source / "file"
        payload.write_text("original")
        with patch.object(scratch, "exchange_paths", side_effect=OSError(errno.EINTR, "switch")):
            with self.assertRaises(OSError):
                scratch.offload_project_scratch(self.source)
        self.assertFalse(self.source.is_symlink())
        self.assertEqual((self.backing / "file").read_text(), "original")
        payload.write_text("updated")
        scratch.offload_project_scratch(self.source)
        self.assertEqual(payload.read_text(), "updated")

    def test_preparation_rejects_a_link_to_an_arbitrary_directory(self):
        self.source.rmdir()
        self.source.symlink_to(self.source.parent)
        with self.assertRaisesRegex(RuntimeError, "unexpected project scratch symlink"):
            scratch.prepare_project_scratch(self.source)
