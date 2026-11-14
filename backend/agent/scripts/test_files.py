"""Verify actual file snapshots, bounded patches and path confinement."""

import base64
import json
import os
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from io import StringIO
from unittest.mock import patch

import files


class FileChangeTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = self.directory.name
        self.filename = os.path.join(self.root, "example.py")

    def write(self, content):
        return files.write_file(self.filename, base64.b64encode(content), self.root)[
            "_change"
        ]

    def test_create_modify_unchanged_and_delete_use_real_contents(self):
        created = self.write(b"before\ncontext\n")
        self.assertEqual(
            (created["kind"], created["added"], created["removed"]), ("created", 2, 0)
        )
        self.assertIn("--- /dev/null\n+++ b/example.py\n", created["patch"])

        modified = self.write(b"after\ncontext\n")
        self.assertIn("-before\n+after\n context\n", modified["patch"])
        self.assertEqual(
            (modified["kind"], modified["added"], modified["removed"]),
            ("modified", 1, 1),
        )
        self.assertEqual(self.write(b"after\ncontext\n")["kind"], "unchanged")

        output = StringIO()
        arguments = ["files.py", self.root, self.root, "example.py", "delete_file", ""]
        with patch.object(sys, "argv", arguments), redirect_stdout(output):
            files.main()
        deleted = json.loads(output.getvalue())["_change"]
        self.assertFalse(os.path.exists(self.filename))
        self.assertEqual(
            (deleted["kind"], deleted["added"], deleted["removed"]), ("deleted", 0, 2)
        )
        self.assertIn("+++ /dev/null", deleted["patch"])

    def test_no_final_newline_and_header_like_content(self):
        self.write(b"old")
        changed = self.write(b"++new")
        self.assertEqual((changed["added"], changed["removed"]), (1, 1))
        self.assertEqual(changed["patch"].count("\\ No newline at end of file"), 2)

    def test_binary_and_large_snapshots_report_why_no_diff_is_available(self):
        for data, notice in [
            (b"\x00binary", "binary"),
            (b"\xff", "binary"),
            (b"x" * (files.MAX_DIFF_INPUT + 1), "too_large"),
        ]:
            with self.subTest(notice=notice):
                if os.path.exists(self.filename):
                    os.unlink(self.filename)
                change = self.write(data)
                self.assertEqual(change["notice"], notice)
                self.assertEqual(change["patch"], "")
                with open(self.filename, "rb") as source:
                    self.assertEqual(source.read(), data)

    def test_patch_limit_keeps_counts_for_the_whole_change(self):
        change = self.write(b"line\n" * 1200)
        self.assertTrue(change["truncated"])
        self.assertEqual(change["added"], 1200)
        self.assertLessEqual(len(change["patch"].splitlines()), files.MAX_PATCH_LINES)

    def test_optional_snapshot_failure_does_not_block_writing(self):
        unavailable = {"exists": True, "text": None, "notice": "unavailable"}
        with patch.object(files, "snapshot_file", return_value=unavailable):
            change = self.write(b"still written\n")
        self.assertEqual(change["notice"], "unavailable")
        with open(self.filename, "rb") as source:
            self.assertEqual(source.read(), b"still written\n")

    def test_realpath_rejects_symlink_escape_and_root_removal(self):
        with tempfile.TemporaryDirectory() as outside:
            os.symlink(outside, os.path.join(self.root, "escape"))
            for name, action in [("escape/file", "write_file"), (".", "delete_file")]:
                arguments = ["files.py", self.root, self.root, name, action, ""]
                with (
                    patch.object(sys, "argv", arguments),
                    self.assertRaises(ValueError),
                ):
                    files.main()

    def test_large_file_contents_travel_over_stdin(self):
        content = b"large file contents\n" * 20000
        arguments = ["files.py", self.root, self.root, "large.txt", "write_file", "-"]
        encoded = StringIO(base64.b64encode(content).decode("ascii"))

        output = StringIO()
        with (
            patch.object(sys, "argv", arguments),
            patch.object(sys, "stdin", encoded),
            redirect_stdout(output),
        ):
            files.main()
        self.assertTrue(json.loads(output.getvalue())["written"])
        with open(os.path.join(self.root, "large.txt"), "rb") as source:
            self.assertEqual(source.read(), content)

    def test_line_ranges_keep_the_full_file_digest_and_exact_content(self):
        content = "first\nGrüße 🦊\nthird\n".encode()
        self.write(content)
        full = files.read_file(self.filename)
        page = files.read_file(self.filename, {"offset": 2, "limit": 1})
        self.assertEqual(page["content"], "Grüße 🦊\n")
        self.assertEqual(page["sha256"], full["sha256"])
        self.assertEqual(
            (page["offset"], page["totalLines"], page["hasMore"]), (2, 3, True)
        )
        self.assertFalse(
            files.read_file(self.filename, {"offset": 3, "limit": 1})["hasMore"]
        )

    def test_special_files_cannot_block_reads_or_write_snapshots(self):
        os.mkfifo(self.filename)
        for operation in (files.read_file, files.snapshot_file):
            with self.assertRaisesRegex(files.ToolInputError, "regular file"):
                operation(self.filename)

    def test_read_rejects_fifo_replacing_a_regular_path_before_open(self):
        self.write(b"original")
        original_open = os.open

        def replace_then_open(filename, flags):
            os.unlink(filename)
            os.mkfifo(filename)
            return original_open(filename, flags)

        with patch.object(files.os, "open", side_effect=replace_then_open):
            with self.assertRaisesRegex(files.ToolInputError, "regular file"):
                files.read_file(self.filename)


if __name__ == "__main__":
    unittest.main()
