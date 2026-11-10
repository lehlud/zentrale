"""Checkout readiness, staged clone failures and preservation on real files."""

import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


specification = importlib.util.spec_from_file_location(
    "repository_helper", Path(__file__).with_name("git.py")
)
repository_helper = importlib.util.module_from_spec(specification)
specification.loader.exec_module(repository_helper)


class RepositoryPreparationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.directory = self.root / "project with 'quotes'"
        self.credentials = self.root / "private credentials"
        self.credentials.mkdir()
        self.origin = self.root / "empty.git"
        subprocess.run(
            ["git", "init", "--bare", str(self.origin)],
            check=True,
            capture_output=True,
        )
        self.source = {
            "id": "root",
            "directory": str(self.directory),
            "url": str(self.origin),
        }

    def run_helper(self, operation, **overrides):
        configuration = {
            **self.source,
            "operation": operation,
            "sources": [self.source],
            "key": "TEST DEPLOYMENT KEY",
            "known": "",
            "temporaryDirectory": str(self.credentials),
            "readOnly": False,
            **overrides,
        }
        return subprocess.run(
            [sys.executable, "-I", str(Path(__file__).with_name("git.py"))],
            input=json.dumps(configuration),
            text=True,
            capture_output=True,
        )

    def test_empty_repository_is_ready_and_existing_checkout_needs_no_credentials(self):
        inspected = self.run_helper("inspect")
        self.assertEqual(inspected.returncode, 0, inspected.stderr)
        self.assertEqual(json.loads(inspected.stdout), ["root"])
        self.assertFalse(self.directory.exists())

        prepared = self.run_helper("prepare")
        self.assertEqual(prepared.returncode, 0, prepared.stderr)
        self.assertTrue((self.directory / ".git").is_dir())
        self.assertEqual(list(self.credentials.iterdir()), [])
        self.assertEqual(list(self.directory.glob(".zentrale-clone-*")), [])
        self.assertEqual(json.loads(self.run_helper("inspect").stdout), [])

        (self.directory / "work.txt").write_text("keep local work")
        reused = self.run_helper("prepare", key=None, temporaryDirectory="/missing")
        self.assertEqual(reused.returncode, 0, reused.stderr)
        self.assertTrue(json.loads(reused.stdout)["existing"])
        self.assertEqual((self.directory / "work.txt").read_text(), "keep local work")

    def test_failed_clone_cleans_staging_and_credentials_and_can_be_retried(self):
        failed = self.run_helper("prepare", url=str(self.root / "missing.git"))
        self.assertNotEqual(failed.returncode, 0)
        self.assertNotIn("TEST DEPLOYMENT KEY", failed.stdout + failed.stderr)
        self.assertEqual(list(self.directory.iterdir()), [])
        self.assertEqual(list(self.credentials.iterdir()), [])
        retried = self.run_helper("prepare")
        self.assertEqual(retried.returncode, 0, retried.stderr)

    def test_nonempty_directory_symlink_and_partial_checkout_are_preserved(self):
        self.directory.mkdir()
        saved = self.directory / "README.md"
        saved.write_text("original")
        for operation in ("inspect", "prepare"):
            result = self.run_helper(operation)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Existing files were preserved", result.stderr)
            self.assertEqual(saved.read_text(), "original")

        alias = self.root / "alias"
        alias.symlink_to(self.directory, target_is_directory=True)
        result = self.run_helper("prepare", directory=str(alias))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("symlinks", result.stderr)
        self.assertEqual(saved.read_text(), "original")

        saved.unlink()
        partial = self.directory / ".zentrale-clone-interrupted"
        partial.mkdir()
        (partial / "download").write_text("incomplete")
        result = self.run_helper("inspect")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Existing files were preserved", result.stderr)
        self.assertEqual((partial / "download").read_text(), "incomplete")

    def test_stalled_git_metadata_check_times_out_and_preserves_work(self):
        prepared = self.run_helper("prepare")
        self.assertEqual(prepared.returncode, 0, prepared.stderr)
        saved = self.directory / "work.txt"
        saved.write_text("local work")

        binaries = self.root / "bin"
        binaries.mkdir()
        git = binaries / "git"
        git.write_text(f"#!{sys.executable}\nimport time\ntime.sleep(5)\n")
        git.chmod(0o700)
        with patch.dict("os.environ", {"PATH": str(binaries)}):
            with self.assertRaisesRegex(
                ValueError, "Timed out inspecting repository"
            ) as failure:
                repository_helper.checkout_exists(self.source, timeout=0.1)
        self.assertIn("rev-parse --show-toplevel", str(failure.exception))
        self.assertIn("Existing files were preserved", str(failure.exception))
        self.assertEqual(saved.read_text(), "local work")
        self.assertTrue(repository_helper.checkout_exists(self.source))


if __name__ == "__main__":
    unittest.main()
