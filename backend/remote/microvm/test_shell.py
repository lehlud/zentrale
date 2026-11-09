"""Guest shell installation and migration, without mounting or changing the host."""

import importlib.util
import json
import subprocess
import sys
from pathlib import Path
import tempfile
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

    def test_installs_fish_once_and_migrates_existing_identity(self):
        (self.root / "usr/bin/microdnf").touch()

        def installed(*args, **kwargs):
            (self.root / "usr/bin/fish").touch()

        with (
            patch.object(guest_init, "mount") as mount,
            patch.object(
                guest_init.subprocess, "run", side_effect=installed
            ) as install,
        ):
            guest_init.prepare_user_shell(self.root)
            guest_init.prepare_user_shell(self.root)
        self.assertEqual(install.call_count, 1)
        self.assertEqual(
            install.call_args.args[0], ["/usr/bin/microdnf", "-y", "install", "fish"]
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
                side_effect=[["git", "ripgrep"], [], []],
            ),
            patch.object(guest_init, "mount") as mount,
            patch.object(guest_init.subprocess, "run") as install,
        ):
            guest_init.prepare_standard_packages(self.root)
            guest_init.prepare_standard_packages(self.root)
        install.assert_called_once()
        self.assertEqual(
            install.call_args.args[0],
            ["/usr/bin/microdnf", "-y", "install", "ca-certificates", "git", "ripgrep"],
        )
        with (
            patch.object(guest_init.os, "chroot") as chroot,
            patch.object(guest_init.os, "chdir") as chdir,
        ):
            install.call_args.kwargs["preexec_fn"]()
            chroot.assert_called_once_with(self.root)
            chdir.assert_called_once_with("/")
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
