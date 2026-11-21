"""Checkout readiness, staged clone failures and preservation on real files."""

import importlib.util
import io
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
        (seed / "assets").mkdir()
        (seed / "assets" / "data.txt").write_text("tracked asset\n")
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

    def test_staging_without_a_receipt_is_unknown_data_and_stays_untouched(self):
        stage, checkout = self.staged_clone()
        (checkout / ".git" / "index.lock").write_bytes(b"partial index")
        (checkout / "README.md").write_text("preserve staging edits")
        (checkout / "draft.txt").write_text("preserve unknown data")

        for operation in ("inspect", "prepare"):
            result = self.run_helper(operation)
            self.assertNotEqual(result.returncode, 0)
            self.assertTrue(stage.exists())
            self.assertEqual((checkout / "README.md").read_text(), "preserve staging edits")
            self.assertEqual((checkout / "draft.txt").read_text(), "preserve unknown data")
            self.assertEqual((checkout / ".git" / "index.lock").read_bytes(), b"partial index")
            self.assertFalse((self.directory / ".git").exists())

    def test_failed_recovery_preserves_the_previous_attempt_for_retry(self):
        stage, checkout = self.staged_clone(receipt=True)
        manifest = stage / "clone.json"
        receipt = json.loads(manifest.read_text())
        receipt["phase"] = "cloning"
        manifest.write_text(json.dumps(receipt))

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

    def test_retry_uses_downloaded_objects_after_checkout_is_interrupted(self):
        self.staged_clone(receipt=True)
        fresh = self.root / "fresh"
        fresh.mkdir()
        source = {**self.source, "directory": str(fresh), "readOnly": False}
        run_clone = repository_helper.run_clone

        def interrupt_checkout(arguments, environment, report_progress, watch_checkout=False):
            if "checkout" in arguments:
                git = Path(arguments[2]) / ".git"
                (git / "index.lock").touch()
                raise subprocess.CalledProcessError(1, arguments)
            return run_clone(arguments, environment, report_progress, watch_checkout)

        with patch.object(repository_helper, "run_clone", interrupt_checkout):
            with self.assertRaises(subprocess.CalledProcessError):
                repository_helper.clone_repository(source, repository_helper.git_environment())
        [receipt] = list(fresh.glob(".zentrale-clone-*/clone.json"))
        saved = json.loads(receipt.read_text())
        self.assertEqual(saved["phase"], "fetched")
        # The simulated first invocation exited once checkout failed.
        receipt.write_text(json.dumps({**saved, "owner": None}))
        self.origin.rename(self.origin.with_name("unavailable.git"))

        resumed = self.run_helper("prepare", directory=str(fresh), reportProgress=True, managedVM=True)
        self.assertEqual(resumed.returncode, 0, resumed.stderr)
        self.assertIn("Checking out files", resumed.stderr)
        self.assertEqual((fresh / "README.md").read_text(), "repository content\n")
        self.assertFalse((fresh / ".git/index.lock").exists())
        self.assertFalse(receipt.exists())

    def test_narrower_checkout_reuses_existing_git_metadata_and_keeps_partial_files(self):
        stage, checkout = self.staged_clone(receipt=True)
        manifest = stage / "clone.json"
        receipt = json.loads(manifest.read_text())
        receipt.update(phase="fetched", entries=[])
        manifest.write_text(json.dumps(receipt))
        (checkout / "large-tree").mkdir()
        (checkout / "large-tree" / "materialized.txt").write_text("previous files")
        self.origin.rename(self.origin.with_name("unavailable.git"))

        invalid = self.run_helper("prepare", checkoutPaths=["typo"])
        self.assertIn("Checkout directory is not in HEAD: typo", invalid.stderr)
        self.assertNotEqual(invalid.returncode, 0)
        result = self.run_helper("prepare", checkoutPaths=["docs"], reportProgress=True, managedVM=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Checking out selected directories", result.stderr)
        self.assertEqual((self.directory / "docs/notes.txt").read_text(), "tracked notes\n")
        self.assertTrue((self.directory / "README.md").exists())
        self.assertFalse((self.directory / "assets").exists())
        self.assertFalse((self.directory / "large-tree").exists())
        preserved = self.directory / ".git/zentrale-clone-recovery" / stage.name
        self.assertTrue((preserved / "repository/large-tree/materialized.txt").exists())
        self.assertEqual(self.git("sparse-checkout", "list"), "docs")
        self.assertEqual(self.git("show", "HEAD:assets/data.txt"), "tracked asset")

    def test_managed_checkout_uses_guest_cpus_within_the_process_limit(self):
        _, checkout = self.staged_clone(receipt=True)
        for cpus, workers in [(16, 0), (288, 252)]:
            with (
                self.subTest(cpus=cpus),
                patch.object(repository_helper.os, "cpu_count", return_value=cpus),
                patch.object(repository_helper.resource, "getrlimit", return_value=(256, 256)),
                patch.object(repository_helper, "run_clone") as clone,
            ):
                repository_helper.checkout_files(
                    checkout, {}, {"managedVM": True}, repository_helper.git_environment(), False,
                )
                self.assertIn(f"checkout.workers={workers}", clone.call_args.args[0])

    def test_large_checkout_publishes_complete_batches_and_git_index(self):
        _, checkout = self.staged_clone(receipt=True)
        unusual = checkout / "file with\na newline"
        unusual.write_text("new contents\n")
        unusual.chmod(0o755)
        link = checkout / "symlink"
        link.symlink_to(unusual.name)
        git = ["git", "-C", str(checkout)]
        environment = repository_helper.git_environment()
        subprocess.run([*git, "add", "--", unusual.name, link.name], env=environment, check=True)
        subprocess.run(
            [*git, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test",
             "commit", "-qm", "add checkout paths"], env=environment, check=True,
        )
        for path in (unusual, link, checkout / "README.md"):
            path.unlink()
        stderr = io.StringIO()
        with (
            patch("sys.stderr", stderr),
            patch.object(repository_helper, "FIRST_CHECKOUT_BATCH", 1),
            patch.object(repository_helper, "CHECKOUT_BATCH", 2),
        ):
            repository_helper.checkout_files(checkout, {}, {"managedVM": True}, environment, True)
        self.assertIn("Updating files 20\t1/5\t", stderr.getvalue())
        self.assertIn("Updating files 60\t3/5\t", stderr.getvalue())
        self.assertIn("Updating files 100\t5/5\t", stderr.getvalue())
        self.assertEqual(unusual.read_text(), "new contents\n")
        self.assertTrue(unusual.stat().st_mode & 0o100)
        self.assertEqual(os.readlink(link), unusual.name)
        self.assertFalse((checkout / ".git/index.lock").exists())
        self.assertEqual(subprocess.check_output([*git, "status", "--porcelain"], env=environment), b"")

    def test_large_checkout_resumes_after_interruption_and_guest_scratch_loss(self):
        stage, checkout = self.staged_clone(receipt=True)
        manifest = stage / "clone.json"
        receipt = json.loads(manifest.read_text())
        receipt.update(phase="fetched", entries=[])
        manifest.write_text(json.dumps(receipt))
        paths = [b"README.md", b"assets/data.txt", b"docs/notes.txt"]
        for name in paths:
            (checkout / os.fsdecode(name)).unlink()
        self.origin.rename(self.origin.with_name("unavailable.git"))
        source = {**self.source, "readOnly": False, "managedVM": True, "reportProgress": True}
        environment = {**repository_helper.git_environment(), "ZENTRALE_SANDBOX": "microvm"}
        scratch = self.root / "guest-scratch"
        scratch.mkdir()
        run_clone = repository_helper.run_clone
        batches = []
        interrupt = True

        def capture(arguments, env, report, watch_checkout=False, stdin=None):
            if "checkout-index" in arguments:
                batches.append(stdin.read().removesuffix(b"\0"))
                stdin.seek(0)
                if interrupt and len(batches) == 2:
                    (checkout / "assets/data.txt").write_bytes(b"partial")
                    raise subprocess.CalledProcessError(-15, arguments)
            return run_clone(arguments, env, report, watch_checkout, stdin)

        with (
            patch.object(repository_helper, "checkout_scratch_directory", return_value=scratch),
            patch.object(repository_helper, "FIRST_CHECKOUT_BATCH", 1),
            patch.object(repository_helper, "CHECKOUT_BATCH", 1),
            patch.object(repository_helper, "run_clone", side_effect=capture),
            patch("sys.stderr", io.StringIO()) as progress,
        ):
            with self.assertRaises(subprocess.CalledProcessError):
                repository_helper.clone_repository(source, environment)
            self.assertEqual(batches, paths[:2])
            checkpoint = checkout / ".git" / repository_helper.CHECKOUT_PROGRESS_FILE
            self.assertEqual(json.loads(checkpoint.read_text())["completed"], 1)

            receipt = json.loads(manifest.read_text())
            receipt["owner"]["boot"] = "previous-guest"
            manifest.write_text(json.dumps(receipt))
            (scratch / "identity").unlink()
            (scratch / "index").unlink()
            interrupt = False
            batches.clear()
            progress.seek(0)
            progress.truncate()
            repository_helper.clone_repository(source, environment)

        self.assertEqual(batches, paths[1:])
        self.assertTrue(progress.getvalue().startswith("ZENTRALE_CLONE_PROGRESS Updating files 33\t1/3\t"))
        self.assertEqual((self.directory / "assets/data.txt").read_text(), "tracked asset\n")
        self.assertEqual(self.git("status", "--porcelain"), "")
        self.assertFalse(stage.exists())
        self.assertFalse((self.directory / ".git" / repository_helper.CHECKOUT_PROGRESS_FILE).exists())

    def test_progress_reports_bytes_when_percentage_remains_constant(self):
        stderr = io.StringIO()
        lines = (
            'Receiving objects: 1% (1/100), 12.00 MiB | 1.00 MiB/s\r'
            'Receiving objects: 1% (1/100), 24.00 MiB | 1.00 MiB/s\r'
            'Updating files: 2% (2/100)\r'
        )
        command = [sys.executable, "-c", f"import sys; sys.stderr.write({lines!r})"]
        with patch("sys.stderr", stderr), patch.object(repository_helper.time, "monotonic", side_effect=[1, 4, 5]):
            repository_helper.run_clone(command, repository_helper.git_environment(), True)
        self.assertIn("Receiving objects 1\t1/100\t12.00 MiB | 1.00 MiB/s", stderr.getvalue())
        self.assertIn("Receiving objects 1\t1/100\t24.00 MiB | 1.00 MiB/s", stderr.getvalue())
        self.assertIn("Updating files 2\t2/100\t", stderr.getvalue())

    @unittest.skipUnless(
        Path(f"/proc/self/task/{os.getpid()}/children").exists(),
        "Linux per-child I/O counters",
    )
    def test_checkout_io_from_workers_remains_visible_without_git_progress_output(self):
        stderr = io.StringIO()
        writer = (
            "import os,sys,time\n"
            "if child := os.fork():\n"
            "    os.waitpid(child, 0)\n"
            "    sys.exit(0)\n"
            "with open(sys.argv[1], 'wb', buffering=0) as output:\n"
            "    for _ in range(5):\n"
            "        output.write(bytes(1024 * 1024))\n"
            "        time.sleep(0.15)\n"
        )
        command = [sys.executable, "-c", writer, str(self.root / "checkout-data")]
        with patch("sys.stderr", stderr), patch.object(repository_helper, "CHECKOUT_ACTIVITY_INTERVAL", 0.05):
            repository_helper.run_clone(command, repository_helper.git_environment(), True, watch_checkout=True)
        self.assertIn("ZENTRALE_CLONE_PROGRESS Writing checkout data -1\t", stderr.getvalue())
        self.assertIn(" MiB", stderr.getvalue())

    @unittest.skipUnless(Path("/proc/self/stat").exists(), "Linux process CPU counters")
    def test_preparing_checkout_signals_activity_without_advancing_progress(self):
        stderr = io.StringIO()
        prepare = "import time\nend = time.process_time() + 0.2\nwhile time.process_time() < end: pass"
        with patch("sys.stderr", stderr), patch.object(repository_helper, "CHECKOUT_ACTIVITY_INTERVAL", 0.03):
            repository_helper.run_clone(
                [sys.executable, "-c", prepare], repository_helper.git_environment(), True, watch_checkout=True,
            )
        self.assertIn("ZENTRALE_CLONE_ACTIVE", stderr.getvalue())
        self.assertNotIn("ZENTRALE_CLONE_PROGRESS", stderr.getvalue())

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

    def test_configuration_ignores_an_abandoned_pending_lock(self):
        self.assertEqual(self.run_helper("prepare").returncode, 0)
        managed = self.directory / ".git/zentrale-git"
        managed.mkdir()
        old_lock = managed / "config.pending.lock"
        old_lock.write_text("interrupted configuration")

        configured = self.configure()
        self.assertEqual(configured.returncode, 0, configured.stderr)
        self.assertEqual(self.git("config", "user.name"), "Project Author")
        self.assertEqual(old_lock.read_text(), "interrupted configuration")
        self.assertEqual(list(managed.glob("config-*")), [])

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

    def test_inspect_updates_a_confirmed_former_origin_and_preserves_the_checkout(self):
        stage, checkout = self.staged_clone(receipt=True)
        old = "git@gitea.example:old/demo.git"
        new = "git@gitea.example:new/demo.git"
        git = ["git", "-C", str(checkout)]
        subprocess.run([*git, "remote", "set-url", "origin", old], check=True)
        head = subprocess.check_output([*git, "rev-parse", "HEAD"])
        edit = checkout / "README.md"
        edit.write_text("uncommitted change")
        source = {**self.source, "directory": str(checkout), "url": new}
        unrelated = self.run_helper("inspect", sources=[{**source, "movedFrom": ["git@gitea.example:other/demo.git"]}])
        self.assertNotEqual(unrelated.returncode, 0)
        self.assertEqual(subprocess.check_output([*git, "config", "--get", "remote.origin.url"]).decode().strip(), old)
        moved = self.run_helper("inspect", sources=[{**source, "movedFrom": [old]}])
        self.assertEqual(moved.returncode, 0, moved.stderr)
        self.assertEqual(json.loads(moved.stdout), [])
        self.assertEqual(subprocess.check_output([*git, "config", "--get", "remote.origin.url"]).decode().strip(), new)
        self.assertEqual(subprocess.check_output([*git, "rev-parse", "HEAD"]), head)
        self.assertEqual(edit.read_text(), "uncommitted change")

    def test_repository_replacement_archives_edits_after_the_new_clone_succeeds(self):
        self.staged_clone(receipt=True)
        self.assertEqual(self.run_helper("prepare").returncode, 0)
        edited = self.directory / "README.md"
        edited.write_text("local edit")
        attached = self.directory / "attachment"
        attached.mkdir()
        (attached / "draft").write_text("keep in place")
        git = ["git", "-C", str(self.directory)]
        previous_head = subprocess.check_output([*git, "rev-parse", "HEAD"])
        new = self.root / "new.git"
        selection = {"url": str(new), "replaceFrom": [str(self.origin)], "attachedNames": [attached.name]}
        result = self.run_helper("inspect", sources=[{**self.source, **selection}])
        self.assertEqual(json.loads(result.stdout), ["root"])
        failed = self.run_helper("prepare", **selection)
        self.assertNotEqual(failed.returncode, 0)
        self.assertEqual(edited.read_text(), "local edit")
        subprocess.run(["git", "clone", "--bare", str(self.origin), str(new)], check=True, capture_output=True)
        prepared = self.run_helper("prepare", **selection)
        self.assertEqual(prepared.returncode, 0, prepared.stderr)
        self.assertEqual(edited.read_text(), "repository content\n")
        history = self.directory / ".git" / repository_helper.ARCHIVE_DIRECTORY
        [saved] = [path for path in history.iterdir() if path.is_dir()]
        self.assertEqual((saved / "README.md").read_text(), "local edit")
        self.assertEqual(subprocess.check_output(["git", "-C", str(saved), "rev-parse", "HEAD"]), previous_head)
        self.assertEqual((attached / "draft").read_text(), "keep in place")
        self.assertEqual(subprocess.check_output([*git, "status", "--porcelain"]), b"?? attachment/\n")

    def test_repository_archive_resumes_after_an_interrupted_rename(self):
        stage, checkout = self.staged_clone(receipt=True)
        old = str(self.root / "old.git")
        source = {**self.source, "replaceFrom": [old]}
        subprocess.run(["git", "init", str(self.directory)], check=True, capture_output=True)
        self.git("remote", "add", "origin", old)
        receipt_file = stage / "clone.json"
        receipt = json.loads(receipt_file.read_text())
        receipt["archiveEntries"] = [".git", "work.txt"]
        receipt_file.write_text(json.dumps(receipt))
        archive_name = repository_helper.ARCHIVE_DIRECTORY
        backup_name = stage.name.removeprefix(".zentrale-clone-")
        backup = checkout / ".git" / archive_name / backup_name
        backup.mkdir(parents=True)
        (self.directory / ".git").rename(backup / ".git")
        (self.directory / "work.txt").write_text("to archive")

        inspected = self.run_helper("inspect", sources=[source])
        self.assertEqual(inspected.returncode, 0, inspected.stderr)
        self.assertEqual(json.loads(inspected.stdout), ["root"])

        prepared = self.run_helper("prepare", replaceFrom=[old])
        self.assertEqual(prepared.returncode, 0, prepared.stderr)
        self.assertFalse(stage.exists())
        saved = self.directory / ".git" / archive_name / backup_name
        self.assertEqual((saved / "work.txt").read_text(), "to archive")
        self.assertTrue((saved / ".git" / "config").is_file())

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
