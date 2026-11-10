"""Checkout readiness, staged clone failures and preservation on real files."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


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

    def configure(self, **overrides):
        source = {
            **self.source,
            "key": "ONLY THIS REPOSITORY KEY",
            "known": "example.test ssh-ed25519 TEST",
            "readOnly": False,
            "identity": {"name": "Project Author", "email": "project@example.test"},
            **overrides,
        }
        return self.run_helper("configure-project-git", project=str(self.directory), sources=[source])

    def git(self, *arguments):
        return subprocess.run(["git", "-C", str(self.directory), *arguments],
                              env={**os.environ, "GIT_CONFIG_GLOBAL": os.devnull,
                                   "GIT_CONFIG_NOSYSTEM": "1"},
                              capture_output=True, text=True, check=True).stdout.strip()

    def test_project_identity_persists_for_plain_git_and_does_not_touch_other_repositories(self):
        self.assertEqual(self.run_helper("prepare").returncode, 0)
        self.git("config", "user.name", "Existing user value")
        self.git("config", "custom.preserved", "yes")
        for _ in range(2):
            configured = self.configure()
            self.assertEqual(configured.returncode, 0, configured.stderr)
        self.assertEqual(self.git("config", "user.name"), "Project Author")
        self.assertEqual(self.git("config", "user.email"), "project@example.test")
        self.assertEqual(self.git("config", "custom.preserved"), "yes")
        self.assertEqual(len(self.git("config", "--local", "--get-all", "include.path").splitlines()), 1)
        self.assertNotIn("ONLY THIS REPOSITORY KEY", self.git("config", "--list"))
        self.assertEqual(self.git("status", "--porcelain"), "")
        # Bare origin and sibling repositories are never given a key or identity.
        self.assertFalse((self.origin / "zentrale-git").exists())
        original = subprocess.run(["git", "--git-dir", str(self.origin), "config", "--local", "--get", "user.name"], capture_output=True)
        self.assertNotEqual(original.returncode, 0)

    def test_repository_scoped_key_strict_host_verification_and_refresh_cleanup(self):
        self.assertEqual(self.run_helper("prepare").returncode, 0)
        url = "ssh://git@example.test:2222/org/repo.git"
        self.git("remote", "set-url", "origin", url)
        configured = self.configure(url=url)
        self.assertEqual(configured.returncode, 0, configured.stderr)
        managed = self.directory / ".git" / "zentrale-git"
        self.assertEqual((managed / "key").read_text(), "ONLY THIS REPOSITORY KEY")
        self.assertEqual(managed.stat().st_mode & 0o777, 0o700)
        self.assertEqual((managed / "key").stat().st_mode & 0o777, 0o600)
        binary = self.root / "bin"
        binary.mkdir()
        fake = binary / "ssh"
        fake.write_text('#!/usr/bin/python3\nimport json,os,pathlib,sys\n'
                        'encoded = json.dumps(sys.argv[1:])\n'
                        'if "FAKE_SSH_LOG" in os.environ:\n'
                        '    pathlib.Path(os.environ["FAKE_SSH_LOG"]).write_text(encoded)\n'
                        '    sys.stdout.write("0000")\n'
                        'else:\n'
                        '    print(encoded)\n')
        fake.chmod(0o700)
        def wrapper(host="git@example.test", command="git-upload-pack '/org/repo.git'"):
            return subprocess.run([sys.executable, "-I", str(managed / "ssh.py"),
                                   "-o", "SendEnv=GIT_PROTOCOL", "-p", "2222", host, command],
                                  env={**os.environ, "PATH": str(binary) + ":" + os.environ["PATH"], "SSH_AUTH_SOCK": "/unrelated-agent"},
                                  text=True, capture_output=True)
        accepted = wrapper()
        self.assertEqual(accepted.returncode, 0, accepted.stderr)
        arguments = json.loads(accepted.stdout)
        for required in ("IdentityAgent=none", "IdentitiesOnly=yes", "StrictHostKeyChecking=yes", "GlobalKnownHostsFile=/dev/null"):
            self.assertIn(required, arguments)
        log = self.root / "ssh-invocation.json"
        plain_git = subprocess.run(["git", "-C", str(self.directory), "ls-remote", "origin"],
                                   env={**os.environ, "PATH": str(binary) + ":" + os.environ["PATH"],
                                        "FAKE_SSH_LOG": str(log), "GIT_CONFIG_GLOBAL": os.devnull,
                                        "GIT_CONFIG_NOSYSTEM": "1"}, capture_output=True, text=True)
        self.assertEqual(plain_git.returncode, 0, plain_git.stderr)
        self.assertEqual(json.loads(log.read_text())[-2:], ["git@example.test", "git-upload-pack '/org/repo.git'"])
        self.assertNotEqual(wrapper(host="git@unrelated.test").returncode, 0)
        self.assertNotEqual(wrapper(command="git-upload-pack '/other.git'").returncode, 0)
        self.assertNotEqual(wrapper(command="git-receive-pack '/org/repo.git'; evil").returncode, 0)
        self.assertEqual(wrapper(command="git-receive-pack '/org/repo.git'").returncode, 0)

        cleared = self.run_helper("clear-project-git", project=str(self.directory))
        self.assertEqual(cleared.returncode, 0, cleared.stderr)
        self.assertFalse((managed / "key").exists())
        self.assertEqual(self.git("config", "core.sshCommand"), "false")
        self.assertEqual(self.configure(url=url, key="ROTATED", readOnly=True).returncode, 0)
        self.assertEqual((managed / "key").read_text(), "ROTATED")
        self.assertNotEqual(wrapper(command="git-receive-pack '/org/repo.git'").returncode, 0)
        self.assertEqual(self.git("remote", "get-url", "--push", "origin"), "DISABLED_READ_ONLY")
        self.assertEqual(self.run_helper("clear-project-git", project=str(self.directory)).returncode, 0)
        removed = self.run_helper("configure-project-git", project=str(self.directory), sources=[])
        self.assertEqual(removed.returncode, 0, removed.stderr)
        self.assertFalse((managed / "key").exists())

    def test_metadata_symlinks_cannot_redirect_credentials(self):
        self.assertEqual(self.run_helper("prepare").returncode, 0)
        outside = self.root / "outside"
        outside.mkdir()
        (self.directory / ".git" / "zentrale-git").symlink_to(outside, target_is_directory=True)
        configured = self.configure()
        self.assertNotEqual(configured.returncode, 0)
        self.assertIn("symlinks", configured.stderr)
        self.assertEqual(list(outside.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
