"""Local checks for host-side trust boundaries; actual KVM is tested over SSH."""

import hashlib
import importlib.util
import io
import json
import os
import subprocess
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile


DIRECTORY = Path(__file__).parent
specification = importlib.util.spec_from_file_location(
    "microvm_runner", DIRECTORY / "runner.py"
)
runner = importlib.util.module_from_spec(specification)
specification.loader.exec_module(runner)


class MicroVMRunnerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.cache = runner.private_directory(self.root / "cache")

    def test_missing_guest_kernel_support_stops_before_boot_without_downgrade(self):
        result = subprocess.CompletedProcess([], 1, "", "modprobe: Module 9p not found")
        with (
            patch.object(runner.shutil, "which", return_value="/usr/sbin/modprobe"),
            patch.object(runner.subprocess, "run", return_value=result) as probe,
        ):
            with self.assertRaisesRegex(
                RuntimeError, "required kernel support is missing: 9p"
            ) as failure:
                runner.check_guest_kernel_modules()
            self.assertNotIsInstance(failure.exception, runner.UnavailableError)
            arguments = probe.call_args.args[0]
            self.assertIn("--dry-run", arguments)
            self.assertIn("--show-depends", arguments)
            self.assertEqual(arguments[arguments.index("--config") + 1], "/dev/null")

    def test_builtin_and_installed_guest_modules_are_supported(self):
        result = subprocess.CompletedProcess([], 0, "builtin virtio_console\n", "")
        with (
            patch.object(runner.shutil, "which", return_value="/usr/sbin/modprobe"),
            patch.object(runner.subprocess, "run", return_value=result),
        ):
            runner.check_guest_kernel_modules()

    def test_bootstrap_failure_retains_a_bounded_diagnostic_after_cleanup(self):
        control = self.root / "bootstrap"
        control.mkdir()
        boot = control / "boot.log"
        boot.write_bytes(b"x" * 20000 + b"\nKernel panic: fs-9p unavailable\n\xff")
        error = runner.bootstrap_failure(
            self.cache, control, "Fedora initialization failed", 255
        )
        boot.unlink()
        diagnostic = json.loads(
            (self.cache / "fedora-bootstrap-failure.json").read_text()
        )
        self.assertEqual(diagnostic["exit"], 255)
        self.assertIn("Kernel panic: fs-9p unavailable", diagnostic["log"])
        self.assertLessEqual(len(diagnostic["log"]), 10000)
        self.assertIn("exit 255", str(error))

    def test_shared_cache_cannot_be_exported_by_another_project(self):
        with patch.object(
            runner, "private_directory", side_effect=lambda directory: directory
        ):
            cache = runner.cache_directory([Path("/home/example/project")])
            self.assertEqual(
                cache, Path("/var/tmp/zentrale-microvm-" + str(os.getuid()))
            )
            for workspace in [Path("/"), Path("/var"), Path("/var/tmp"), cache]:
                with self.subTest(workspace=workspace), self.assertRaises(RuntimeError):
                    runner.cache_directory([workspace])

    def test_cold_boot_failure_keeps_disk_and_diagnostics_for_retry(self):
        scope = self.root / "scope"
        control = scope / "session"
        control.mkdir(parents=True)
        disk = scope / "root.qcow2"
        disk.write_bytes(b"existing guest files")
        (control / "boot.log").write_text("x" * 20000 + "\nInstalling 129/130\n")
        # The supervisor's generic exit must not hide PID 1's specific cause.
        (control / "failure.json").write_text(
            '{"error":"MicroVM stopped unexpectedly"}'
        )
        (control / "startup-failure.json").write_text(
            '{"reason":"package_install_timeout"}'
        )
        failure = runner.startup_failure(scope, control)
        self.assertIsInstance(failure, runner.StartupFailure)
        self.assertEqual(failure.reason, "package_install_timeout")
        runner.stop_session(scope)
        self.assertFalse(control.exists())
        self.assertEqual(disk.read_bytes(), b"existing guest files")
        diagnostic = json.loads((scope / "startup-failure.json").read_text())
        self.assertLessEqual(len(diagnostic["output"]), 10000)
        self.assertIn("Installing 129/130", diagnostic["output"])

    def test_unknown_boot_errors_do_not_become_retryable(self):
        control = self.root / "session"
        control.mkdir()
        (control / "boot.log").write_text("invalid guest configuration")
        (control / "startup-failure.json").write_text('{"reason":"unknown"}')
        self.assertNotIsInstance(
            runner.startup_failure(self.root, control), runner.StartupFailure
        )

    def test_cache_rejects_symlinks_and_shared_directory_permissions(self):
        link = self.root / "link"
        link.symlink_to(self.cache, target_is_directory=True)
        with self.assertRaises(RuntimeError):
            runner.private_directory(link)
        self.cache.chmod(0o755)
        with self.assertRaises(RuntimeError):
            runner.private_directory(self.cache)

    def test_download_is_bounded_and_checked_before_extraction(self):
        data = b"untrusted executable"
        with patch.object(
            runner.urllib.request, "urlopen", return_value=io.BytesIO(data)
        ):
            with self.assertRaisesRegex(RuntimeError, "SHA-256"):
                runner.download(
                    {"url": "https://example.test/runtime", "sha256": "0" * 64}
                )
        with patch.object(runner, "MAX_DOWNLOAD_BYTES", 8):
            with patch.object(
                runner.urllib.request, "urlopen", return_value=io.BytesIO(data)
            ):
                with self.assertRaises(RuntimeError):
                    runner.download(
                        {
                            "url": "https://example.test/runtime",
                            "sha256": hashlib.sha256(data).hexdigest(),
                        }
                    )

    def test_wheel_cannot_write_outside_installation_or_create_symlinks(self):
        for name, mode in [
            ("../../escaped", stat.S_IFREG | 0o700),
            ("link", stat.S_IFLNK | 0o700),
        ]:
            archive = io.BytesIO()
            with zipfile.ZipFile(archive, "w") as wheel:
                entry = zipfile.ZipInfo(name)
                entry.external_attr = mode << 16
                wheel.writestr(entry, "outside")
            release = {"version": "test", "runtime": {}, "busybox": {}}
            with patch.object(runner, "download", return_value=archive.getvalue()):
                with self.assertRaises(RuntimeError):
                    runner.install_runtime(self.cache, release)
            self.assertFalse((self.root / "escaped").exists())
            self.assertFalse((self.cache / "test").exists())

    def test_verified_installation_is_atomic_and_reused(self):
        archive = io.BytesIO()
        with zipfile.ZipFile(archive, "w") as wheel:
            wheel.writestr("virtme/guest/virtme-init", "fixture")
        package = io.BytesIO()
        with tarfile.open(fileobj=package, mode="w:gz") as bundle:
            member = tarfile.TarInfo("bin/busybox.static")
            member.size = 7
            bundle.addfile(member, io.BytesIO(b"busybox"))
        release = {
            "version": "test",
            "runtime": {},
            "busybox": {"member": "bin/busybox.static"},
        }
        with patch.object(
            runner, "download", side_effect=[archive.getvalue(), package.getvalue()]
        ) as download:
            runtime = runner.install_runtime(self.cache, release)
            self.assertEqual(runner.install_runtime(self.cache, release), runtime)
            self.assertEqual(download.call_count, 2)

        self.assertEqual((runtime / "busybox").read_bytes(), b"busybox")
        self.assertEqual(stat.S_IMODE((runtime / "busybox").stat().st_mode), 0o700)
        self.assertEqual(json.loads((runtime / "installed.json").read_text()), release)

    def test_command_data_and_host_paths_never_become_hypervisor_options(self):
        workspace = self.root / "project 'quoted',option = value"
        temporary = self.root / "temporary"
        workspace.mkdir()
        temporary.mkdir()
        runtime = self.cache / "runtime"
        runtime.mkdir()
        (runtime / "busybox").write_bytes(b"busybox")
        command = "#" + "x" * 150000 + "\nprintf done\n"
        request = {
            "workspace": str(workspace),
            "temporaryDirectory": str(temporary),
            "command": command,
            "network": False,
        }
        request.update(agentId="agent-a", projectId="project-a")
        scope = runner.scope_directory(self.cache, request)
        control = scope / "session"
        configuration = {
            "guest": "guest",
            "init": "init",
            "streams": "streams",
            "terminal": "terminal",
            "runnerHash": "fixture",
        }
        runner.prepare_control(control, configuration, runtime, request)
        with patch.object(runner, "ensure_session", return_value=control):
            invocation = runner.stage(
                self.cache, request, configuration, runtime, None, None
            )
        self.assertEqual((invocation / "command.sh").read_text(), command)
        self.assertEqual((control / "workspace").resolve(), workspace)
        self.assertFalse(runner.within(workspace, invocation))
        with patch.object(
            runner.shutil, "which", return_value="/usr/bin/qemu-system-x86_64"
        ):
            arguments = runner.virtme_arguments(
                runtime, Path("/boot/kernel"), control, request
            )
        self.assertNotIn(command, arguments)
        self.assertFalse(any(str(workspace) in argument for argument in arguments))
        self.assertIn("microvm", arguments)
        self.assertIn("--show-boot-console", arguments)
        self.assertNotIn("--disable-kvm", arguments)
        # The persistent VM has a NIC. Per-action network namespaces enforce
        # individual approvals without rebooting or inheriting a past grant.
        self.assertIn("--net", arguments)

        request["provision"] = True
        with patch.object(
            runner.shutil, "which", return_value="/usr/bin/qemu-system-x86_64"
        ):
            self.assertNotIn(
                "--net",
                runner.virtme_arguments(
                    runtime, Path("/boot/kernel"), control, request
                ),
            )

    def test_reused_pid_is_not_treated_as_the_old_supervisor(self):
        reference = runner.process_reference()
        self.assertTrue(runner.alive(json.dumps(reference)))
        reference["started"] = str(int(reference["started"]) - 1)
        self.assertFalse(runner.alive(json.dumps(reference)))
        self.assertFalse(runner.alive("not a process"))
        self.assertFalse(runner.alive(str(os.getpid())))

    def cached_session(self):
        scope = self.root / "agent"
        control = scope / "session"
        (control / "requests").mkdir(parents=True)
        configuration = {"runnerHash": "fixture", "vmType": "microvm"}
        (control / "version").write_text(runner.session_identity(configuration, {}))
        (control / "supervisor.pid").write_text(json.dumps(runner.process_reference()))
        (control / "ready.json").write_text("{}")
        (control / "request.json").write_text(json.dumps({"vmType": "microvm"}))
        return scope, control, configuration

    def test_cached_supervisor_does_not_prove_guest_dispatcher_is_alive(self):
        scope, control, configuration = self.cached_session()

        class RestartRequested(Exception):
            pass

        with (
            patch.object(runner, "stop_session", side_effect=RestartRequested) as stop,
            patch.object(runner, "guest_responding", return_value=False),
        ):
            with self.assertRaises(RestartRequested):
                runner.ensure_session(scope, configuration, None, None, None, {})
            stop.assert_called_once_with(scope)
        (control / "heartbeat").touch()
        with (
            patch.object(runner, "stop_session") as stop,
            patch.object(runner, "guest_responding", return_value=True),
        ):
            self.assertEqual(
                runner.ensure_session(scope, configuration, None, None, None, {}),
                control,
            )
            stop.assert_not_called()

    def test_healthy_running_jobs_defer_an_implementation_change(self):
        scope, control, configuration = self.cached_session()
        job = control / "requests" / "job"
        job.mkdir()
        (job / "started").touch()
        (job / "client.pid").write_text(json.dumps(runner.process_reference()))
        for updated in (configuration, dict(configuration, runnerHash="updated")):
            with (
                self.subTest(updated=updated),
                patch.object(runner, "stop_session") as stop,
                patch.object(runner, "guest_responding", return_value=True),
            ):
                self.assertEqual(
                    runner.ensure_session(scope, updated, None, None, None, {}), control
                )
                stop.assert_not_called()
        for responsive, changed, reason in [
            (False, configuration, "unresponsive"),
            (True, dict(configuration, vmType="q35"), "configuration_changed"),
        ]:
            with (
                self.subTest(reason=reason),
                patch.object(runner, "guest_responding", return_value=responsive),
                patch.object(runner, "stop_session") as stop,
            ):
                with self.assertRaises(runner.RecoveryRequired) as failure:
                    runner.ensure_session(scope, changed, None, None, None, {})
                self.assertEqual(failure.exception.reason, reason)
                stop.assert_not_called()
        (job / "cancel").touch()
        self.assertFalse(runner.active_invocations(control))
        (job / "cancel").unlink()
        (job / "status").write_text("0")
        self.assertFalse(runner.active_invocations(control))

    def test_health_reply_proves_progress_despite_clock_skew(self):
        import threading
        import time

        _, control, _ = self.cached_session()
        (control / "heartbeat").touch()
        os.utime(control / "heartbeat", (1, 1))

        def respond():
            while not (control / "health.request").exists():
                time.sleep(0.001)
            nonce = json.loads((control / "health.request").read_text())["nonce"]
            runner.atomic_json(control / "health.response", {"nonce": nonce})

        worker = threading.Thread(target=respond)
        worker.start()
        self.assertTrue(runner.guest_responding(control))
        worker.join(timeout=1)
        self.assertFalse(worker.is_alive())

    def test_stale_health_reply_does_not_prove_progress(self):
        _, control, _ = self.cached_session()
        (control / "heartbeat").touch()
        runner.atomic_json(control / "health.response", {"nonce": "old"})
        with (
            patch.object(runner.time, "monotonic", side_effect=[0, 0, 6]),
            patch.object(runner.time, "sleep"),
        ):
            self.assertFalse(runner.guest_responding(control))

    def test_scope_is_stable_but_separates_agents_and_projects(self):
        execution = {
            "agentId": "agent-a",
            "projectId": "project-a",
            "workspace": "/work/project",
            "temporaryDirectory": "/tmp/agent-a",
        }
        first = runner.scope_directory(self.cache, execution)
        self.assertEqual(first, runner.scope_directory(self.cache, execution))
        self.assertNotEqual(
            first,
            runner.scope_directory(self.cache, dict(execution, agentId="agent-b")),
        )
        self.assertNotEqual(
            first,
            runner.scope_directory(self.cache, dict(execution, projectId="project-b")),
        )
        with self.assertRaises(RuntimeError):
            runner.scope_directory(
                self.cache, dict(execution, workspace="/another/project")
            )
        for forged in ("../agent-b", "", "/agent", "a" * 81):
            with self.subTest(forged=forged), self.assertRaises(RuntimeError):
                runner.scope_directory(self.cache, dict(execution, agentId=forged))


if __name__ == "__main__":
    unittest.main()
