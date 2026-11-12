"""Exercise the exact embedded Python CLI protocols in real subprocesses."""

import base64
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parent
FILES = (SCRIPTS / "files.py").read_text()
PATCH = (
    "import sys, types\n"
    "file_helpers = types.ModuleType('zentrale_files')\n"
    "exec(" + repr(FILES) + ", file_helpers.__dict__)\n"
    "sys.modules['zentrale_files'] = file_helpers\n"
    + (SCRIPTS / "patch.py").read_text()
)


class ToolCLITests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)

    def invoke(self, source, arguments, data=b"", inject=""):
        if inject:
            marker = 'if __name__ == "__main__":'
            # Patch CLI embeds files.py as a quoted library; inject only at
            # the final entry point, never inside that embedded source string.
            before, after = source.rsplit(marker, 1)
            source = before + inject + "\n" + marker + after
        return subprocess.run(
            [sys.executable, "-I", "-c", source, str(self.root),
             str(self.root / "tmp"), *arguments],
            input=data, capture_output=True, timeout=5,
        )

    def file(self, action, path, content=b"", inject=""):
        return self.invoke(FILES, [path, action, "-"],
                           base64.b64encode(content), inject)

    def patch(self, body, inject=""):
        data = body if isinstance(body, bytes) else (
            "*** Begin Patch\n" + body + "*** End Patch\n"
        ).encode()
        return self.invoke(PATCH, [], data, inject)

    def expected_error(self, process, code, message):
        self.assertEqual(process.returncode, 0, process.stderr)
        self.assertEqual(process.stderr, b"")
        result = json.loads(process.stdout)
        self.assertEqual(set(result), {"_toolError"})
        self.assertEqual(result["_toolError"]["code"], code)
        self.assertIn(message, result["_toolError"]["message"])
        self.assertNotIn("Traceback", process.stdout.decode())
        return result["_toolError"]["message"]

    def test_missing_read_list_delete_have_concise_path_errors(self):
        for action in ("read_file", "list_files", "delete_file"):
            with self.subTest(action=action):
                message = self.expected_error(
                    self.file(action, "missing.txt"), "ENOENT", "path not found")
                self.assertIn("missing.txt", message)

    def test_file_and_directory_errors_leave_no_partial_writes(self):
        (self.root / "blocker").write_text("original")
        self.expected_error(self.file("write_file", "blocker/child", b"new"),
                            "EEXIST", "already exists")
        (self.root / "directory").mkdir()
        self.expected_error(self.file("read_file", "directory"), "EISDIR", "directory")
        self.expected_error(self.file("write_file", "directory", b"new"), "EISDIR", "directory")
        self.assertEqual((self.root / "blocker").read_text(), "original")
        self.assertEqual(sorted(p.name for p in self.root.iterdir()), ["blocker", "directory"])

    def test_successful_write_and_read_keep_existing_protocol(self):
        written = self.file("write_file", "one.txt", b"contents\n")
        self.assertEqual(written.returncode, 0, written.stderr)
        self.assertTrue(json.loads(written.stdout)["written"])
        read = self.file("read_file", "one.txt")
        self.assertEqual(read.stderr, b"")
        self.assertEqual(json.loads(read.stdout)["content"], "contents\n")

    def test_patch_context_or_missing_target_does_not_apply_prior_valid_change(self):
        (self.root / "one.txt").write_text("one\n")
        (self.root / "two.txt").write_text("two\n")
        for target, old, expected in (("two.txt", "outdated", "context did not match"),
                                      ("missing.txt", "missing", "target does not exist")):
            with self.subTest(target=target):
                message = self.expected_error(self.patch(
                    "*** Update File: one.txt\n@@\n-one\n+changed\n"
                    f"*** Update File: {target}\n@@\n-{old}\n+new\n"),
                    "invalid_input", expected)
                self.assertIn(target, message)
                self.assertEqual((self.root / "one.txt").read_text(), "one\n")
                self.assertEqual((self.root / "two.txt").read_text(), "two\n")
                self.assertEqual(len(list(self.root.iterdir())), 2)

    def test_patch_encoding_and_validation_errors_remain_short(self):
        (self.root / "binary.txt").write_bytes(b"\xff\n")
        self.expected_error(self.patch("*** Update File: binary.txt\n@@\n-old\n+new\n"),
                            "invalid_input", "not valid UTF-8")
        self.expected_error(self.patch(b"\xff"), "invalid_input", "input must be valid UTF-8")
        self.expected_error(self.patch(b"not a patch"), "invalid_input", "must start with")
        self.assertEqual((self.root / "binary.txt").read_bytes(), b"\xff\n")

    def test_invalid_content_and_long_messages_are_bounded_without_writing(self):
        (self.root / "one.txt").write_text("original\n")
        self.expected_error(self.invoke(FILES, ["one.txt", "write_file", "-"], b"not-base64!"),
                            "invalid_input", "valid base64")
        self.assertEqual((self.root / "one.txt").read_text(), "original\n")
        message = self.expected_error(self.file("read_file", "one.txt", inject=(
            "def read_file(*args):\n raise ToolInputError('long\\n' * 1000)")),
            "invalid_input", "long\\n")
        self.assertLessEqual(len(message), 800)
        self.assertNotIn("\n", message)

    def test_expected_permission_failure_uses_same_subprocess_error_protocol(self):
        # Inject the kernel's EACCES result at the syscall boundary so this
        # assertion also works in CI environments running the test as root.
        inject = "def read_file(*args):\n raise PermissionError(13, 'Permission denied', 'private.txt')"
        self.expected_error(self.file("read_file", "private.txt", inject=inject),
                            "EACCES", "permission denied")

    def test_patch_write_failure_restores_originals_and_reports_operational_error(self):
        for name in ("one.txt", "two.txt"):
            (self.root / name).write_text(name + "\n")
        inject = (
            "original_replace = os.replace\nreplace_calls = 0\n"
            "def replace_once(source, destination):\n"
            " global replace_calls\n replace_calls += 1\n"
            " if replace_calls == 2:\n  raise PermissionError(13, 'Permission denied', destination)\n"
            " return original_replace(source, destination)\n"
            "os.replace = replace_once"
        )
        self.expected_error(self.patch(
            "*** Update File: one.txt\n@@\n-one.txt\n+changed\n"
            "*** Update File: two.txt\n@@\n-two.txt\n+changed\n", inject),
            "EACCES", "original files were restored")
        for name in ("one.txt", "two.txt"):
            self.assertEqual((self.root / name).read_text(), name + "\n")
        self.assertEqual(len(list(self.root.iterdir())), 2)

    def test_unexpected_internal_exceptions_keep_tracebacks_and_nonzero_exit(self):
        for expression in ("ValueError('internal invariant broke')",
                           "OSError(9, 'Bad file descriptor')"):
            with self.subTest(expression=expression):
                process = self.file("read_file", "one.txt", inject=(
                    "def read_file(*args):\n raise " + expression))
                self.assertNotEqual(process.returncode, 0)
                self.assertEqual(process.stdout, b"")
                self.assertIn(b"Traceback", process.stderr)
        (self.root / "one.txt").write_text("one\n")
        process = self.patch("*** Update File: one.txt\n@@\n-one\n+new\n",
                             "def updated_content(*args):\n return 1 / 0")
        self.assertNotEqual(process.returncode, 0)
        self.assertIn(b"ZeroDivisionError", process.stderr)
        self.assertIn(b"Traceback", process.stderr)
        self.assertEqual((self.root / "one.txt").read_text(), "one\n")


if __name__ == "__main__":
    unittest.main()
