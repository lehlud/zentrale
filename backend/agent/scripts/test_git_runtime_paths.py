"""Portable project Git configuration, exercised through the real helper CLI."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


HELPER = Path(__file__).with_name("git.py").resolve()


class GitRuntimePathTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="git paths 'quoted' ")
        self.addCleanup(temporary.cleanup)
        self.project = Path(temporary.name) / "project"
        self.scratch = Path(temporary.name) / "scratch"
        self.project.mkdir()
        self.scratch.mkdir()
        for directory in (self.project, self.project / "additional"):
            directory.mkdir(exist_ok=True)
            subprocess.run(
                ["git", "init", str(directory)], check=True, capture_output=True
            )
            subprocess.run(
                ["git", "-C", str(directory), "remote", "add", "origin",
                 "git@example.test:org/repository.git"],
                check=True,
                capture_output=True,
            )

    def helper(self, configuration):
        result = subprocess.run(
            [sys.executable, "-I", str(HELPER)],
            cwd=self.project,
            env={**os.environ, "TMPDIR": str(self.scratch)},
            input=json.dumps(configuration),
            text=True,
            capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return result

    def source(self, directory):
        return {
            "directory": directory,
            "url": "git@example.test:org/repository.git",
            "httpUrls": [],
            "readOnly": False,
            "key": "TEST PRIVATE KEY",
            "known": "TEST KNOWN HOST",
            "identity": {"name": "Agent", "email": "agent@example.test"},
        }

    def test_relative_root_and_additional_source_use_actual_working_directory(self):
        self.helper({
            "operation": "configure-project-git",
            "project": ".",
            "sources": [self.source("."), self.source("./additional")],
        })
        for directory in (self.project, self.project / "additional"):
            credentials = directory / ".git" / "zentrale-git"
            self.assertEqual((credentials / "key").read_text(), "TEST PRIVATE KEY")
            local = (directory / ".git" / "config").read_text()
            self.assertIn(str(credentials / "config"), local)
            config = (credentials / "config").read_text()
            self.assertIn("ssh.py", config)
            self.assertIn("agent@example.test", config)

        self.helper({"operation": "clear-project-git", "project": "."})
        for directory in (self.project, self.project / "additional"):
            credentials = directory / ".git" / "zentrale-git"
            self.assertFalse((credentials / "key").exists())
            self.assertFalse((credentials / "known_hosts").exists())
            self.assertIn("sshCommand = false", (credentials / "config").read_text())

    def configure_sources(self):
        self.helper({
            "operation": "configure-project-git", "project": ".",
            "sources": [self.source("."), self.source("./additional")],
        })

    def assert_revoked(self):
        for directory in (self.project, self.project / "additional"):
            credentials = directory / ".git" / "zentrale-git"
            for secret in ("key", "known_hosts", "ssh.py", "destination.json"):
                self.assertFalse((credentials / secret).exists())
            result = subprocess.run(
                ["git", "-C", str(directory), "config", "--get", "core.sshCommand"],
                check=True, capture_output=True, text=True,
            )
            self.assertEqual(result.stdout.strip(), "false")
            includes = subprocess.run(
                ["git", "-C", str(directory), "config", "--local", "--get-all", "include.path"],
                check=True, capture_output=True, text=True,
            ).stdout.splitlines()
            self.assertEqual(includes.count(str(credentials / "config")), 1)

    def test_inventory_remains_authoritative_and_relative_after_a_move(self):
        self.configure_sources()
        manifest = self.project / ".git" / ".zentrale-project-git" / "sources.json"
        self.assertEqual(json.loads(manifest.read_text()), [".", "additional"])
        moved = self.project.with_name("moved project")
        self.project.rename(moved)
        self.project = moved
        for _ in range(2):
            self.helper({"operation": "clear-project-git", "project": "."})
        self.assert_revoked()

    def test_existing_storage_paths_are_mapped_only_within_this_project(self):
        self.configure_sources()
        previous_root = self.project
        manifest = self.project / ".git" / ".zentrale-project-git" / "sources.json"
        manifest.write_text(json.dumps([str(previous_root), str(previous_root / "additional")]))
        moved = self.project.with_name("guest export")
        self.project.rename(moved)
        self.project = moved
        self.helper({
            "operation": "clear-project-git", "project": ".",
            "storageRoot": str(previous_root),
        })
        self.assert_revoked()

    def test_inventory_cannot_revoke_external_or_traversal_paths(self):
        self.configure_sources()
        manifest = self.project / ".git" / ".zentrale-project-git" / "sources.json"
        for recorded in (str(self.scratch), "../scratch"):
            manifest.write_text(json.dumps([recorded]))
            result = subprocess.run(
                [sys.executable, "-I", str(HELPER)], cwd=self.project,
                input=json.dumps({"operation": "clear-project-git", "project": "."}),
                text=True, capture_output=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("outside project", result.stderr)
        self.assertEqual(
            (self.project / ".git" / "zentrale-git" / "key").read_text(),
            "TEST PRIVATE KEY",
        )

    def test_symlinked_inventory_fails_before_touching_keys(self):
        self.configure_sources()
        manifest = self.project / ".git" / ".zentrale-project-git" / "sources.json"
        outside = self.scratch / "inventory.json"
        manifest.rename(outside)
        manifest.symlink_to(outside)
        result = subprocess.run(
            [sys.executable, "-I", str(HELPER)], cwd=self.project,
            input=json.dumps({"operation": "clear-project-git", "project": "."}),
            text=True, capture_output=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must not be a symlink", result.stderr)
        self.assertTrue((self.project / ".git" / "zentrale-git" / "key").exists())

    def test_git_command_uses_the_execution_scratch_directory(self):
        configuration = self.source(".")
        configuration.update(operation="command", arguments=["status", "--porcelain"])
        self.helper(configuration)
        self.assertEqual(list(self.scratch.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
