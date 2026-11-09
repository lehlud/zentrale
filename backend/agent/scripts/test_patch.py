"""Codex-format changes, confinement, staging and recovery on actual files."""

import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch as mock_patch

import files

sys.modules["zentrale_files"] = files
import patch as patch_tool


class PatchTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)

    def apply(self, body):
        return patch_tool.apply_patch(
            str(self.root),
            str(self.root / "tmp"),
            "*** Begin Patch\n" + body + "*** End Patch\n",
        )

    def test_add_update_multiple_chunks_move_and_delete(self):
        result = self.apply(
            "*** Add File: example.py\n+def one():\n+    return 1\n+\n+def two():\n+    return 2\n*** Add File: unused.txt\n+unused\n"
        )
        self.assertEqual(len(result["_changes"]), 2)
        self.assertEqual(result["files"][0]["kind"], "created")
        os.chmod(self.root / "example.py", 0o750)
        result = self.apply(
            "*** Update File: example.py\n*** Move to: src/example.py\n@@ def one():\n-    return 1\n+    return 10\n@@ def two():\n-    return 2\n+    return 20\n*** End of File\n*** Delete File: unused.txt\n"
        )
        self.assertEqual(
            (self.root / "src/example.py").read_text(),
            "def one():\n    return 10\n\ndef two():\n    return 20\n",
        )
        self.assertEqual(os.stat(self.root / "src/example.py").st_mode & 0o777, 0o750)
        self.assertFalse((self.root / "example.py").exists())
        self.assertFalse((self.root / "unused.txt").exists())
        self.assertEqual(len(result["_changes"]), 3)

    def test_late_context_failure_keeps_every_original_file(self):
        (self.root / "one.txt").write_text("one\n")
        (self.root / "two.txt").write_text("two\n")
        with self.assertRaisesRegex(ValueError, "context did not match"):
            self.apply(
                "*** Update File: one.txt\n@@\n-one\n+changed\n*** Update File: two.txt\n@@\n-not-the-current-content\n+changed\n"
            )
        self.assertEqual((self.root / "one.txt").read_text(), "one\n")
        self.assertEqual((self.root / "two.txt").read_text(), "two\n")

    def test_rejects_escapes_symlinks_roots_and_existing_destinations(self):
        (self.root / "one.txt").write_text("one\n")
        with tempfile.TemporaryDirectory() as outside:
            (self.root / "escape").symlink_to(outside, target_is_directory=True)
            for body in [
                "*** Add File: ../outside.txt\n+bad\n",
                "*** Add File: escape/outside.txt\n+bad\n",
                "*** Delete File: .\n",
                "*** Add File: one.txt\n+overwrite\n",
                "*** Update File: one.txt\n*** Move to: one.txt\n@@\n-one\n+overwrite\n",
            ]:
                with self.subTest(body=body), self.assertRaises(ValueError):
                    self.apply(body)
            self.assertEqual(list(Path(outside).iterdir()), [])
        self.assertEqual((self.root / "one.txt").read_text(), "one\n")

    def test_preserves_crlf_unicode_and_final_newline_choice(self):
        (self.root / "text.txt").write_bytes("Grüße\r\nworld\r\n".encode())
        self.apply("*** Update File: text.txt\n@@\n-world\n+Welt 🦊\n")
        self.assertEqual(
            (self.root / "text.txt").read_bytes(), "Grüße\r\nWelt 🦊\r\n".encode()
        )
        (self.root / "text.txt").write_bytes(b"before")
        self.apply("*** Update File: text.txt\n@@\n-before\n+after\n")
        self.assertEqual((self.root / "text.txt").read_bytes(), b"after")

    def test_failed_second_commit_restores_earlier_files(self):
        for name in ("one.txt", "two.txt"):
            (self.root / name).write_text(name + "\n")
        replace = os.replace
        calls = 0

        def fail_second(source, target):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise OSError("disk error")
            return replace(source, target)

        with (
            mock_patch.object(patch_tool.os, "replace", side_effect=fail_second),
            self.assertRaisesRegex(OSError, "original files were restored"),
        ):
            self.apply(
                "*** Update File: one.txt\n@@\n-one.txt\n+changed\n*** Update File: two.txt\n@@\n-two.txt\n+changed\n"
            )
        for name in ("one.txt", "two.txt"):
            self.assertEqual((self.root / name).read_text(), name + "\n")
        self.assertEqual(
            sorted(item.name for item in self.root.iterdir()), ["one.txt", "two.txt"]
        )

    def test_does_not_overwrite_an_edit_made_during_preparation(self):
        filename = self.root / "one.txt"
        filename.write_text("one\n")
        stage = patch_tool.stage_file

        def concurrent_edit(*arguments):
            filename.write_text("user edit\n")
            return stage(*arguments)

        with (
            mock_patch.object(patch_tool, "stage_file", side_effect=concurrent_edit),
            self.assertRaisesRegex(ValueError, "changed during preparation"),
        ):
            self.apply("*** Update File: one.txt\n@@\n-one\n+agent edit\n")
        self.assertEqual(filename.read_text(), "user edit\n")

    def test_rejects_invalid_and_oversized_patch_without_writing(self):
        for text in [
            "bad",
            "*** Begin Patch\n*** Add File: a\nnot-prefixed\n*** End Patch",
            "x" * (patch_tool.MAX_PATCH_BYTES + 1),
        ]:
            with self.subTest(text=text[:30]), self.assertRaises(ValueError):
                patch_tool.apply_patch(str(self.root), str(self.root / "tmp"), text)
        self.assertEqual(list(self.root.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
