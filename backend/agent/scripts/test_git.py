"""Checkout readiness, staged clone failures and preservation on real files."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
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

    def staged_clone(self, receipt=False):
        seed = self.root / "seed"
        subprocess.run(
            ["git", "init", "-b", "main", str(seed)], check=True, capture_output=True
        )
        (seed / "README.md").write_text("repository content\n")
        (seed / "docs").mkdir()
        (seed / "docs" / "notes.txt").write_text("tracked notes\n")
        for arguments in (
            ["add", "."],
            [
                "-c",
                "user.name=Fixture",
                "-c",
                "user.email=fixture@example.test",
                "-c",
                "commit.gpgsign=false",
                "commit",
                "-m",
                "Initial content",
            ],
            ["push", str(self.origin), "HEAD:refs/heads/main"],
        ):
            subprocess.run(
                ["git", "-C", str(seed), *arguments],
                check=True,
                capture_output=True,
                env=repository_helper.git_environment(),
            )
        subprocess.run(
            [
                "git",
                "--git-dir",
                str(self.origin),
                "symbolic-ref",
                "HEAD",
                "refs/heads/main",
            ],
            check=True,
            capture_output=True,
        )
        self.directory.mkdir()
        stage = Path(tempfile.mkdtemp(prefix=".zentrale-clone-", dir=self.directory))
        checkout = stage / "repository"
        subprocess.run(
            ["git", "clone", str(self.origin), str(checkout)],
            check=True,
            capture_output=True,
            env=repository_helper.git_environment(),
        )
        if receipt:
            (stage / "clone.json").write_text(
                json.dumps(
                    {
                        "version": 1,
                        "directory": str(self.directory),
                        "url": str(self.origin),
                        "phase": "ready",
                        "entries": [entry.name for entry in checkout.iterdir()],
                        "owner": None,
                    }
                )
            )
        return stage, checkout

    def test_interrupted_legacy_checkout_is_retained_and_rebuilt(self):
        stage, checkout = self.staged_clone()
        (checkout / ".git" / "index").unlink()
        (checkout / ".git" / "index.lock").write_bytes(b"partial index")
        (checkout / "README.md").write_text("preserve staging edits")
        (checkout / "draft.txt").write_text("preserve unknown data")
        (checkout / "docs" / "notes.txt").unlink()
        inspected = self.run_helper("inspect")
        self.assertEqual(inspected.returncode, 0, inspected.stderr)
        self.assertEqual(json.loads(inspected.stdout), ["root"])

        prepared = self.run_helper("prepare")
        self.assertEqual(prepared.returncode, 0, prepared.stderr)
        self.assertEqual(
            (self.directory / "README.md").read_text(), "repository content\n"
        )
        self.assertEqual(
            (self.directory / "docs" / "notes.txt").read_text(), "tracked notes\n"
        )
        self.assertFalse((self.directory / ".git" / "index.lock").exists())
        self.assertFalse(
            (self.directory / ".git" / "objects" / "info" / "alternates").exists()
        )
        saved = (
            self.directory
            / ".git"
            / "zentrale-clone-recovery"
            / stage.name
            / "repository"
        )
        self.assertEqual((saved / "README.md").read_text(), "preserve staging edits")
        self.assertEqual((saved / "draft.txt").read_text(), "preserve unknown data")
        self.assertEqual((saved / ".git" / "index.lock").read_bytes(), b"partial index")
        self.assertEqual(list(self.directory.glob(".zentrale-clone-*")), [])
        self.assertEqual(self.git("status", "--porcelain"), "")

    def test_failed_recovery_preserves_the_previous_attempt_for_retry(self):
        stage, checkout = self.staged_clone()
        saved = checkout / "draft.txt"
        saved.write_text("retain this")
        unavailable = self.origin.with_name("unavailable.git")
        self.origin.rename(unavailable)
        failed = self.run_helper("prepare")
        self.assertNotEqual(failed.returncode, 0)
        self.assertEqual(saved.read_text(), "retain this")
        self.assertEqual(list(self.directory.iterdir()), [stage])
        unavailable.rename(self.origin)
        self.assertEqual(self.run_helper("prepare").returncode, 0)

    def test_live_clone_is_left_alone_then_recovered_after_its_process_exits(self):
        if not Path("/proc/sys/kernel/random/boot_id").exists():
            self.skipTest("Linux clone ownership protocol")
        self.directory.mkdir()
        stage = Path(tempfile.mkdtemp(prefix=".zentrale-clone-", dir=self.directory))
        (stage / "repository").mkdir()
        (stage / "repository" / "draft.txt").write_text("keep partial checkout")
        owner = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])

        def stop(process):
            if process.poll() is None:
                process.kill()
            process.wait(timeout=5)

        self.addCleanup(stop, owner)
        started = (
            Path(f"/proc/{owner.pid}/stat").read_text().rsplit(")", 1)[1].split()[19]
        )
        (stage / "clone.json").write_text(
            json.dumps(
                {
                    "version": 1,
                    "directory": str(self.directory),
                    "url": str(self.origin),
                    "phase": "cloning",
                    "entries": [],
                    "owner": {
                        "boot": Path("/proc/sys/kernel/random/boot_id")
                        .read_text()
                        .strip(),
                        "pid": owner.pid,
                        "started": started,
                    },
                }
            )
        )
        configuration = {
            **self.source,
            "operation": "prepare",
            "key": "TEST KEY",
            "known": "",
            "temporaryDirectory": str(self.credentials),
            "readOnly": False,
        }
        helper = subprocess.Popen(
            [sys.executable, "-I", str(Path(__file__).with_name("git.py"))],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.addCleanup(stop, helper)
        helper.stdin.write(json.dumps(configuration))
        helper.stdin.close()
        helper.stdin = None
        time.sleep(0.15)
        self.assertIsNone(helper.poll())
        self.assertFalse((self.directory / ".git").exists())
        self.assertEqual(
            (stage / "repository" / "draft.txt").read_text(), "keep partial checkout"
        )
        owner.terminate()
        owner.wait(timeout=5)
        _, error = helper.communicate(timeout=10)
        self.assertEqual(helper.returncode, 0, error)
        saved = self.directory / ".git" / "zentrale-clone-recovery" / stage.name
        self.assertEqual(
            (saved / "repository" / "draft.txt").read_text(), "keep partial checkout"
        )

    def test_publication_resumes_without_network_and_preserves_moved_file_edits(self):
        stage, checkout = self.staged_clone(receipt=True)
        (checkout / "README.md").rename(self.directory / "README.md")
        (self.directory / "README.md").write_text("edited after publication started")
        self.origin.rename(self.origin.with_name("unavailable.git"))
        inspected = self.run_helper("inspect")
        self.assertEqual(inspected.returncode, 0, inspected.stderr)
        self.assertEqual(json.loads(inspected.stdout), ["root"])
        prepared = self.run_helper("prepare")
        self.assertEqual(prepared.returncode, 0, prepared.stderr)
        self.assertEqual(
            (self.directory / "README.md").read_text(),
            "edited after publication started",
        )
        self.assertEqual(
            (self.directory / "docs" / "notes.txt").read_text(), "tracked notes\n"
        )
        self.assertTrue((self.directory / ".git").is_dir())
        self.assertFalse(stage.exists())

    def test_finished_publication_is_cleaned_during_configuration(self):
        stage, checkout = self.staged_clone(receipt=True)
        for entry in sorted(checkout.iterdir(), key=lambda path: path.name == ".git"):
            entry.rename(self.directory / entry.name)
        self.assertEqual(json.loads(self.run_helper("inspect").stdout), [])
        configured = self.configure()
        self.assertEqual(configured.returncode, 0, configured.stderr)
        self.assertFalse(stage.exists())

    def test_publication_conflict_never_overwrites_existing_files(self):
        stage, checkout = self.staged_clone(receipt=True)
        (self.directory / "README.md").write_text("existing user file")
        prepared = self.run_helper("prepare")
        self.assertNotEqual(prepared.returncode, 0)
        self.assertIn("publication conflicts", prepared.stderr)
        self.assertEqual(
            (self.directory / "README.md").read_text(), "existing user file"
        )
        self.assertEqual((checkout / "README.md").read_text(), "repository content\n")
        self.assertTrue((checkout / ".git").is_dir())

    def test_unrelated_receipts_and_symlinked_checkouts_are_not_adopted(self):
        stage, checkout = self.staged_clone(receipt=True)
        receipt = json.loads((stage / "clone.json").read_text())
        receipt["url"] = str(self.root / "unrelated.git")
        (stage / "clone.json").write_text(json.dumps(receipt))
        rejected = self.run_helper("inspect")
        self.assertNotEqual(rejected.returncode, 0)
        self.assertIn("Existing files were preserved", rejected.stderr)
        receipt["url"] = str(self.origin)
        (stage / "clone.json").write_text(json.dumps(receipt))
        outside = self.root / "outside"
        checkout.rename(outside)
        checkout.symlink_to(outside, target_is_directory=True)
        rejected = self.run_helper("prepare")
        self.assertNotEqual(rejected.returncode, 0)
        self.assertEqual((outside / "README.md").read_text(), "repository content\n")

    def configure(self, **overrides):
        source = {
            **self.source,
            "key": "ONLY THIS REPOSITORY KEY",
            "known": "example.test ssh-ed25519 TEST",
            "readOnly": False,
            "identity": {"name": "Project Author", "email": "project@example.test"},
            **overrides,
        }
        return self.run_helper(
            "configure-project-git", project=str(self.directory), sources=[source]
        )

    def git(self, *arguments):
        return subprocess.run(
            ["git", "-C", str(self.directory), *arguments],
            env={
                **os.environ,
                "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_CONFIG_NOSYSTEM": "1",
            },
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()

    def test_project_identity_persists_for_plain_git_and_does_not_touch_other_repositories(
        self,
    ):
        self.assertEqual(self.run_helper("prepare").returncode, 0)
        self.git("config", "user.name", "Existing user value")
        self.git("config", "custom.preserved", "yes")
        for _ in range(2):
            configured = self.configure()
            self.assertEqual(configured.returncode, 0, configured.stderr)
        self.assertEqual(self.git("config", "user.name"), "Project Author")
        self.assertEqual(self.git("config", "user.email"), "project@example.test")
        self.assertEqual(self.git("config", "custom.preserved"), "yes")
        self.assertEqual(
            len(
                self.git("config", "--local", "--get-all", "include.path").splitlines()
            ),
            1,
        )
        self.assertNotIn("ONLY THIS REPOSITORY KEY", self.git("config", "--list"))
        self.assertEqual(self.git("status", "--porcelain"), "")
        # Bare origin and sibling repositories are never given a key or identity.
        self.assertFalse((self.origin / "zentrale-git").exists())
        original = subprocess.run(
            [
                "git",
                "--git-dir",
                str(self.origin),
                "config",
                "--local",
                "--get",
                "user.name",
            ],
            capture_output=True,
        )
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
        fake.write_text(
            "#!/usr/bin/python3\nimport json,os,pathlib,sys\n"
            "encoded = json.dumps(sys.argv[1:])\n"
            'if "FAKE_SSH_LOG" in os.environ:\n'
            '    pathlib.Path(os.environ["FAKE_SSH_LOG"]).write_text(encoded)\n'
            '    sys.stdout.write("0000")\n'
            "else:\n"
            "    print(encoded)\n"
        )
        fake.chmod(0o700)

        def wrapper(host="git@example.test", command="git-upload-pack '/org/repo.git'"):
            return subprocess.run(
                [
                    sys.executable,
                    "-I",
                    str(managed / "ssh.py"),
                    "-o",
                    "SendEnv=GIT_PROTOCOL",
                    "-p",
                    "2222",
                    host,
                    command,
                ],
                env={
                    **os.environ,
                    "PATH": str(binary) + ":" + os.environ["PATH"],
                    "SSH_AUTH_SOCK": "/unrelated-agent",
                },
                text=True,
                capture_output=True,
            )

        accepted = wrapper()
        self.assertEqual(accepted.returncode, 0, accepted.stderr)
        arguments = json.loads(accepted.stdout)
        for required in (
            "IdentityAgent=none",
            "IdentitiesOnly=yes",
            "StrictHostKeyChecking=yes",
            "GlobalKnownHostsFile=/dev/null",
        ):
            self.assertIn(required, arguments)
        log = self.root / "ssh-invocation.json"
        plain_git = subprocess.run(
            ["git", "-C", str(self.directory), "ls-remote", "origin"],
            env={
                **os.environ,
                "PATH": str(binary) + ":" + os.environ["PATH"],
                "FAKE_SSH_LOG": str(log),
                "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_CONFIG_NOSYSTEM": "1",
            },
            capture_output=True,
            text=True,
        )
        self.assertEqual(plain_git.returncode, 0, plain_git.stderr)
        self.assertEqual(
            json.loads(log.read_text())[-2:],
            ["git@example.test", "git-upload-pack '/org/repo.git'"],
        )
        self.assertNotEqual(wrapper(host="git@unrelated.test").returncode, 0)
        self.assertNotEqual(
            wrapper(command="git-upload-pack '/other.git'").returncode, 0
        )
        self.assertNotEqual(
            wrapper(command="git-receive-pack '/org/repo.git'; evil").returncode, 0
        )
        self.assertEqual(
            wrapper(command="git-receive-pack '/org/repo.git'").returncode, 0
        )

        cleared = self.run_helper("clear-project-git", project=str(self.directory))
        self.assertEqual(cleared.returncode, 0, cleared.stderr)
        self.assertFalse((managed / "key").exists())
        self.assertEqual(self.git("config", "core.sshCommand"), "false")
        self.assertEqual(
            self.configure(url=url, key="ROTATED", readOnly=True).returncode, 0
        )
        self.assertEqual((managed / "key").read_text(), "ROTATED")
        self.assertNotEqual(
            wrapper(command="git-receive-pack '/org/repo.git'").returncode, 0
        )
        self.assertEqual(
            self.git("remote", "get-url", "--push", "origin"), "DISABLED_READ_ONLY"
        )
        self.assertEqual(
            self.run_helper(
                "clear-project-git", project=str(self.directory)
            ).returncode,
            0,
        )
        removed = self.run_helper(
            "configure-project-git", project=str(self.directory), sources=[]
        )
        self.assertEqual(removed.returncode, 0, removed.stderr)
        self.assertFalse((managed / "key").exists())

    def test_metadata_symlinks_cannot_redirect_credentials(self):
        self.assertEqual(self.run_helper("prepare").returncode, 0)
        outside = self.root / "outside"
        outside.mkdir()
        (self.directory / ".git" / "zentrale-git").symlink_to(
            outside, target_is_directory=True
        )
        configured = self.configure()
        self.assertNotEqual(configured.returncode, 0)
        self.assertIn("symlinks", configured.stderr)
        self.assertEqual(list(outside.iterdir()), [])

    def test_https_origin_is_repaired_only_for_the_selected_repository(self):
        self.assertEqual(self.run_helper("prepare").returncode, 0)
        url = "git@github.com:team/demo.git"
        aliases = [
            "https://github.com/team/demo",
            "https://github.com/team/demo.git",
        ]
        for origin in [*aliases, "https://old-token@github.com/team/demo.git"]:
            with self.subTest(origin=origin):
                self.git("config", "--local", "remote.origin.url", origin)
                configured = self.configure(url=url, httpUrls=None)
                self.assertEqual(configured.returncode, 0, configured.stderr)
                self.assertEqual(self.git("config", "remote.origin.url"), url)
                for alias in aliases:
                    self.assertEqual(self.git("ls-remote", "--get-url", alias), url)

        for origin in (
            "https://github.com/team/another.git",
            "https://github.com/team/demo-other.git",
            "https://unrelated.test/team/demo.git",
            "git@github.com:team/demo",
        ):
            with self.subTest(origin=origin):
                self.git("config", "--local", "remote.origin.url", origin)
                configured = self.configure(url=url)
                self.assertNotEqual(configured.returncode, 0)
                self.assertIn("origin does not match", configured.stderr)
                self.assertEqual(self.git("config", "remote.origin.url"), origin)

    def test_writable_source_removes_only_its_own_read_only_hook(self):
        prepared = self.run_helper("prepare", readOnly=True)
        self.assertEqual(prepared.returncode, 0, prepared.stderr)
        hook = self.directory / ".git" / "hooks" / "pre-push"
        self.assertEqual(hook.read_bytes(), repository_helper.READ_ONLY_HOOK)
        configured = self.configure()
        self.assertEqual(configured.returncode, 0, configured.stderr)
        self.assertFalse(hook.exists())
        self.assertEqual(
            self.git("remote", "get-url", "--push", "origin"), str(self.origin)
        )

        custom_hook = b"#!/bin/sh\necho 'User hook' >&2\nexit 1\n"
        hook.write_bytes(custom_hook)
        hook.chmod(0o700)
        configured = self.configure()
        self.assertEqual(configured.returncode, 0, configured.stderr)
        self.assertEqual(hook.read_bytes(), custom_hook)

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
