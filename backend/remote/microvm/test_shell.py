"""Guest shell installation and migration, without mounting or changing the host."""

import importlib.util
import contextlib
import io
import json
import os
import subprocess
import sys
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch


def load_module(name, filename):
    spec = importlib.util.spec_from_file_location(
        name, Path(__file__).with_name(filename)
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


guest_init = load_module("guest_init", "init.py")
guest = load_module("guest_execution", "guest.py")


class GuestShellTests(unittest.TestCase):
    def test_commands_and_terminal_use_clean_guest_environment(self):
        session = {"workspace": "/host/private/project-id", "temporaryDirectory": "/host/private/scratch", "uid": 1000, "gid": 1000}
        with (
            patch.object(guest, "mount"),
            patch.object(guest, "login_shell", return_value="/usr/bin/fish"),
            patch.object(guest.os, "chroot") as chroot,
            patch.object(guest.os, "chdir") as chdir,
            patch.object(guest.os, "fchown"),
            patch.object(guest, "drop_privileges"),
            patch.object(guest.resource, "setrlimit"),
            patch.object(guest.os, "execve") as execute,
        ):
            guest.execute({"network": True, "tty": True}, session, "pwd")
        chroot.assert_called_once_with(guest.FEDORA)
        chdir.assert_called_once_with("/home/zentrale/project")
        environment = execute.call_args.args[2]
        self.assertEqual(environment["HOME"], "/home/zentrale")
        self.assertEqual(environment["TMPDIR"], "/tmp/zentrale")
        self.assertEqual(environment["XDG_CACHE_HOME"], "/tmp/zentrale/cache")
        self.assertEqual(environment["TERM"], "xterm-256color")
        self.assertNotIn("/host/", json.dumps(environment))
        os.close(int(execute.call_args.args[1][1].rsplit("/", 1)[1]))

    def test_guest_project_paths_are_stable_and_preserve_compatibility(self):
        request = {
            "workspace": "/home/host-user/.local/share/zentrale/projects/project-id",
            "temporaryDirectory": "/tmp/host-scratch-id",
        }
        exports = guest_init.project_exports(request)
        self.assertIn(("workspace", "/home/zentrale/project"), exports)
        self.assertIn(("temporary", "/tmp/zentrale"), exports)
        self.assertIn(("workspace", request["workspace"]), exports)
        self.assertIn(("temporary", request["temporaryDirectory"]), exports)
        self.assertEqual(len(exports), 4)
        canonical = {"workspace": "/home/zentrale/project", "temporaryDirectory": "/tmp/zentrale"}
        self.assertEqual(len(guest_init.project_exports(canonical)), 2)
        self.assertEqual(request["workspace"], "/home/host-user/.local/share/zentrale/projects/project-id")

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        for directory in ("usr/bin", "etc", "proc"):
            (self.root / directory).mkdir(parents=True)
        self.passwd = self.root / "etc/passwd"
        self.passwd.write_text(
            "root:x:0:0:root:/root:/bin/bash\nzentrale:x:1000:1000:Agent:/home/zentrale:/bin/bash\n"
        )

    def test_dispatcher_echoes_only_the_current_health_nonce(self):
        nonce = "a" * 32
        (self.root / "health.request").write_text(json.dumps({"nonce": nonce}))
        guest_init.respond_to_health_check(self.root)
        self.assertEqual(
            json.loads((self.root / "health.response").read_text()), {"nonce": nonce}
        )
        (self.root / "health.request").write_text(json.dumps({"nonce": "invalid"}))
        guest_init.respond_to_health_check(self.root)
        self.assertEqual(
            json.loads((self.root / "health.response").read_text()), {"nonce": nonce}
        )

    def test_installs_fish_once_and_migrates_existing_identity(self):
        (self.root / "usr/bin/microdnf").touch()

        def installed(*args, **kwargs):
            (self.root / "usr/bin/fish").touch()

        with (
            patch.object(guest_init, "mount") as mount,
            patch.object(
                guest_init, "run_package_manager", side_effect=installed
            ) as install,
        ):
            guest_init.prepare_user_shell(self.root)
            guest_init.prepare_user_shell(self.root)
        self.assertEqual(install.call_count, 1)
        self.assertEqual(
            install.call_args.args[0],
            [
                "/usr/bin/microdnf",
                "-y",
                "--setopt=install_weak_deps=0",
                "install",
                "fish",
            ],
        )
        mount.assert_called_once()
        self.assertIn(
            "zentrale:x:1000:1000:Agent:/home/zentrale:/usr/bin/fish",
            self.passwd.read_text(),
        )
        self.assertIn("root:x:0:0:root:/root:/bin/bash", self.passwd.read_text())
        self.assertEqual((self.root / "etc/shells").read_text(), "/usr/bin/fish\n")
        self.assertEqual(guest.login_shell(self.root, False, False), "/usr/bin/fish")
        self.assertEqual(guest.login_shell(self.root, True, False), "/bin/sh")
        self.assertEqual(guest.login_shell(self.root, False, True), "/bin/sh")

    def test_preserves_an_explicit_other_shell(self):
        (self.root / "usr/bin/fish").touch()
        self.passwd.write_text(
            self.passwd.read_text().replace(
                "/home/zentrale:/bin/bash", "/home/zentrale:/usr/bin/zsh"
            )
        )
        guest_init.prepare_user_shell(self.root)
        self.assertEqual(guest.login_shell(self.root, False, False), "/usr/bin/zsh")

    def test_installs_only_missing_standard_tools_without_changing_existing_disk(self):
        (self.root / "usr/bin/microdnf").touch()
        state = self.root / "saved-project-state"
        state.write_text("retain me")
        with (
            patch.object(
                guest_init,
                "missing_standard_packages",
                side_effect=[["git-core", "ripgrep"], [], []],
            ),
            patch.object(guest_init, "mount") as mount,
            patch.object(guest_init, "run_package_manager") as install,
        ):
            guest_init.prepare_standard_packages(self.root)
            guest_init.prepare_standard_packages(self.root)
        install.assert_called_once()
        self.assertEqual(
            install.call_args.args[0],
            [
                "/usr/bin/microdnf",
                "-y",
                "--setopt=install_weak_deps=0",
                "install",
                "ca-certificates",
                "git-core",
                "ripgrep",
            ],
        )
        self.assertEqual(install.call_args.args[1], self.root)
        mount.assert_called_once()
        self.assertEqual(state.read_text(), "retain me")

    def test_standard_tool_probe_uses_guest_path_and_deduplicates_packages(self):
        result = subprocess.CompletedProcess(
            [], 0, "openssh-clients\nopenssh-clients\nripgrep\n", ""
        )
        with patch.object(guest_init.subprocess, "run", return_value=result) as probe:
            missing = guest_init.missing_standard_packages(self.root)
        self.assertEqual(missing, ["ripgrep", "openssh-clients"])
        self.assertEqual(probe.call_args.kwargs["env"], guest_init.GUEST_ENV)
        with (
            patch.object(guest_init.os, "chroot") as chroot,
            patch.object(guest_init.os, "chdir"),
        ):
            probe.call_args.kwargs["preexec_fn"]()
        chroot.assert_called_once_with(self.root)

    def test_complete_standard_tools_do_not_contact_package_repositories(self):
        with (
            patch.object(guest_init, "missing_standard_packages", return_value=[]),
            patch.object(guest_init, "install_guest_packages") as install,
        ):
            guest_init.prepare_standard_packages(self.root)
        install.assert_not_called()

    def test_interrupted_transaction_replays_even_after_commands_appear(self):
        (self.root / "usr/bin/microdnf").touch()
        saved = self.root / "user-data"
        saved.write_text("keep this")
        failure = guest_init.PackageInstallFailure("package_install_timeout", "timeout")
        with (
            patch.object(guest_init, "mount"),
            patch.object(
                guest_init,
                "missing_standard_packages",
                side_effect=[["git-core"], [], []],
            ),
            patch.object(
                guest_init, "run_package_manager", side_effect=[failure, None]
            ) as install,
        ):
            with self.assertRaises(guest_init.PackageInstallFailure):
                guest_init.prepare_standard_packages(self.root)
            self.assertEqual(
                guest_init.pending_packages(self.root), ["ca-certificates", "git-core"]
            )
            guest_init.prepare_standard_packages(self.root)
        self.assertEqual(install.call_args_list[0], install.call_args_list[1])
        self.assertEqual(guest_init.pending_packages(self.root), [])
        self.assertEqual(saved.read_text(), "keep this")

    def test_package_journal_cannot_follow_a_symlink_out_of_the_guest(self):
        with tempfile.TemporaryDirectory() as outside:
            (self.root / "var").symlink_to(outside)
            with self.assertRaisesRegex(RuntimeError, "leaves the guest"):
                guest_init.pending_packages(self.root)
            self.assertEqual(list(Path(outside).iterdir()), [])

    def run_transaction(self, script, **timeouts):
        output = io.StringIO()
        with (
            patch.object(guest_init, "enter_guest", return_value=None),
            contextlib.redirect_stdout(output),
        ):
            guest_init.run_package_manager(
                [sys.executable, "-u", "-c", script], self.root, **timeouts
            )
        return output.getvalue()

    def test_package_progress_extends_inactivity_budget(self):
        output = self.run_transaction(
            "import time\nfor i in range(6):\n print('installing', flush=True)\n time.sleep(0.1)",
            timeout=3,
            idle_timeout=0.4,
        )
        self.assertEqual(output.count("installing"), 6)

    def test_silent_package_transaction_times_out(self):
        with self.assertRaises(guest_init.PackageInstallFailure) as failure:
            self.run_transaction(
                "import time; time.sleep(60)", timeout=3, idle_timeout=0.1
            )
        self.assertEqual(failure.exception.reason, "package_install_timeout")

    def test_progress_cannot_extend_total_package_budget(self):
        with self.assertRaises(guest_init.PackageInstallFailure):
            self.run_transaction(
                "import time\nwhile True:\n print('progress', flush=True)\n time.sleep(0.02)",
                timeout=0.2,
                idle_timeout=1,
            )

    def test_nonzero_package_exit_is_a_retryable_setup_failure(self):
        with self.assertRaises(guest_init.PackageInstallFailure) as failure:
            self.run_transaction("raise SystemExit(1)")
        self.assertEqual(failure.exception.reason, "package_install_failed")

    @unittest.skipUnless(
        sys.platform == "linux", "Linux package transaction process groups"
    )
    def test_timeout_also_kills_a_package_script_descendant(self):
        script = "import signal,os,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); print(os.getpid(),flush=True); time.sleep(60)"
        parent = (
            "import subprocess,sys,time; subprocess.Popen([sys.executable,'-u','-c',"
            + repr(script)
            + "]); time.sleep(60)"
        )
        output = io.StringIO()
        with (
            patch.object(guest_init, "enter_guest", return_value=None),
            contextlib.redirect_stdout(output),
            self.assertRaises(guest_init.PackageInstallFailure),
        ):
            guest_init.run_package_manager(
                [sys.executable, "-u", "-c", parent],
                self.root,
                timeout=2,
                idle_timeout=0.4,
            )
        pid = int(output.getvalue().strip())
        for _ in range(20):
            try:
                # A killed child can briefly await reaping by the host's PID 1.
                if (
                    Path(f"/proc/{pid}/stat").read_text().split(")", 1)[1].split()[0]
                    == "Z"
                ):
                    return
                os.kill(pid, 0)
            except (FileNotFoundError, ProcessLookupError):
                return
            time.sleep(0.05)
        self.fail("package script survived transaction timeout")

    def test_unsuccessful_standard_install_cannot_be_reported_as_ready(self):
        with (
            patch.object(
                guest_init, "missing_standard_packages", return_value=["ripgrep"]
            ),
            patch.object(guest_init, "install_guest_packages"),
        ):
            with self.assertRaisesRegex(RuntimeError, "still missing: ripgrep"):
                guest_init.prepare_standard_packages(self.root)

    def test_failed_install_does_not_change_the_login_shell(self):
        original = self.passwd.read_text()
        with self.assertRaisesRegex(RuntimeError, "cannot install fish"):
            guest_init.prepare_user_shell(self.root)
        self.assertEqual(self.passwd.read_text(), original)

    def test_terminal_relay_is_selected_inside_the_task_pid_namespace(self):
        job = self.root / "job"
        job.mkdir()
        (job / "request.json").write_text(json.dumps({"network": True, "tty": True}))
        (job / "command.sh").write_text("python3 -i")
        (self.root / "request.json").write_text("{}")
        with (
            patch.object(guest, "CONTROL", str(self.root)),
            patch.object(sys, "argv", ["guest.py", str(job)]),
            patch.object(guest.LIBC, "unshare", return_value=0),
            patch.object(guest, "mount"),
            patch.object(guest.runpy, "run_path") as load,
        ):
            from unittest.mock import Mock

            relay = Mock(return_value=7)
            load.return_value = {"run_terminal": relay}
            self.assertEqual(guest.main(), 7)
        self.assertTrue(relay.call_args.kwargs["namespace_init"])


if __name__ == "__main__":
    unittest.main()
