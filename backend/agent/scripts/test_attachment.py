import hashlib
import importlib.util
import io
import os
from pathlib import Path
import tempfile
import subprocess
import sys
import time
import unittest

spec = importlib.util.spec_from_file_location("attachment", Path(__file__).with_name("attachment.py"))
attachment = importlib.util.module_from_spec(spec)
spec.loader.exec_module(attachment)


class AttachmentTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def upload(self, data=b"\x00\xff\r\n", receipt="receipt", name="binary.dat", **kwargs):
        attachment.upload(str(self.root), receipt, name, kwargs.get("size", len(data)),
                          kwargs.get("digest", hashlib.sha256(data).hexdigest()), io.BytesIO(data))

    def test_binary_empty_and_idempotence(self):
        self.upload()
        self.upload()
        self.assertEqual((self.root / "attachments/receipt/binary.dat").read_bytes(), b"\x00\xff\r\n")
        self.upload(b"", receipt="empty")
        self.assertEqual((self.root / "attachments/empty/binary.dat").stat().st_size, 0)
        with self.assertRaises(ValueError):
            self.upload(b"different")
        with self.assertRaises(ValueError):
            self.upload(name="different-name.dat")
        self.assertFalse(list(self.root.glob("attachments/*/.upload-*")))

    def test_traversal_and_limits(self):
        for name in ("", ".", "..", "../escape", "a/b", "a\\b", "bad\x00"):
            with self.assertRaises(ValueError):
                self.upload(name=name)
        for receipt in ("../bad", "", "/tmp/bad", "a" * 129, "é"):
            with self.assertRaises(ValueError):
                self.upload(receipt=receipt)
        with self.assertRaises(ValueError):
            self.upload(size=attachment.LIMIT + 1)

    def test_incomplete_oversized_digest_and_cleanup(self):
        for options in ({"size": 20}, {"size": 1}, {"digest": "0" * 64}):
            with self.assertRaises(ValueError):
                self.upload(**options)
            self.assertFalse((self.root / "attachments/receipt/binary.dat").exists())
            self.assertFalse(list(self.root.glob("attachments/*/.upload-*")))

    def test_symlinks_at_every_level(self):
        victim = self.root / "victim"
        victim.mkdir()
        rootlink = self.root / "rootlink"
        rootlink.symlink_to(victim, target_is_directory=True)
        with self.assertRaises(OSError):
            attachment.upload(str(rootlink), "id", "file", 0, hashlib.sha256(b"").hexdigest(), io.BytesIO())
        attachments = self.root / "attachments"
        attachments.symlink_to(victim, target_is_directory=True)
        with self.assertRaises(OSError):
            self.upload()
        attachments.unlink()
        attachments.mkdir()
        (attachments / "receipt").symlink_to(victim, target_is_directory=True)
        with self.assertRaises(OSError):
            self.upload()
        (attachments / "receipt").unlink()
        (attachments / "receipt").mkdir()
        target = victim / "protected"
        target.write_bytes(b"secret")
        (attachments / "receipt/binary.dat").symlink_to(target)
        with self.assertRaises(OSError):
            self.upload()
        self.assertEqual(target.read_bytes(), b"secret")
        self.assertFalse(list(self.root.glob("attachments/*/.upload-*")))

    def test_fifo_is_not_followed_or_blocking(self):
        target = self.root / "attachments/receipt"
        target.mkdir(parents=True)
        os.mkfifo(target / "binary.dat")
        with self.assertRaises(ValueError):
            self.upload()

    def test_presence_probe_and_missing_file_restore(self):
        data = b"original"
        digest = hashlib.sha256(data).hexdigest()
        self.assertFalse(attachment.present(str(self.root), "receipt", "binary.dat", len(data), digest))
        self.upload(data)
        self.assertTrue(attachment.present(str(self.root), "receipt", "binary.dat", len(data), digest))
        (self.root / "attachments/receipt/binary.dat").unlink()
        self.upload(data)
        self.assertTrue(attachment.present(str(self.root), "receipt", "binary.dat", len(data), digest))

    def test_manifest_binds_original_display_name_before_database_receipt(self):
        data = b"same data"
        digest = hashlib.sha256(data).hexdigest()
        attachment.upload(str(self.root), "receipt", "a_b.txt", len(data), digest, io.BytesIO(data), "a b.txt")
        self.assertTrue(attachment.present(str(self.root), "receipt", "a_b.txt", len(data), digest, "a b.txt"))
        with self.assertRaises(ValueError):
            attachment.present(str(self.root), "receipt", "a_b.txt", len(data), digest, "a?b.txt")

    def test_cancelled_process_removes_staging_files(self):
        process = subprocess.Popen([sys.executable, "-I", str(Path(__file__).with_name("attachment.py")), str(self.root), "cancelled", "binary.dat", "20", "0" * 64], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(lambda: process.kill() if process.poll() is None else None)
        deadline = time.monotonic() + 3
        while not list(self.root.glob("attachments/*/.upload-*")) and time.monotonic() < deadline:
            time.sleep(0.01)
        self.assertIsNone(process.poll())
        process.terminate()
        process.communicate(timeout=3)
        self.assertNotEqual(process.returncode, 0)
        self.assertFalse(list(self.root.glob("attachments/*/.upload-*")))


if __name__ == "__main__":
    unittest.main()
