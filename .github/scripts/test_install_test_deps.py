import os
import pathlib
import subprocess
import tempfile
import unittest


class TestDependencyInstallation(unittest.TestCase):
    def run_install(self, failure=""):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            sudo = root / "sudo"
            sudo.write_text(
                '#!/bin/sh\n'
                'printf "%s\\n" "$*" >> "$CALL_LOG"\n'
                'case "$*" in\n'
                '  "apt-get update") phase=update ;;\n'
                '  "apt-get install "*) phase=install ;;\n'
                '  *) exit 99 ;;\n'
                'esac\n'
                'if [ "$FAIL_PHASE" = "$phase" ]; then\n'
                '  printf "E: %s failed at 100%%\\n" "$phase"\n'
                '  exit 100\n'
                'fi\n'
                'printf "ok %s\\n" "$phase"\n'
            )
            sudo.chmod(0o700)
            log = root / "calls"
            environment = dict(os.environ, PATH=directory + os.pathsep + os.environ["PATH"], CALL_LOG=str(log), FAIL_PHASE=failure)
            result = subprocess.run(
                ["sh", str(pathlib.Path(__file__).with_name("install-test-deps.sh")), "g++", "fish"],
                env=environment, text=True, capture_output=True, timeout=5,
            )
            return result, log.read_text().splitlines()

    def test_success(self):
        result, calls = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["apt-get update", "apt-get install -y --no-install-recommends g++ fish"])
        self.assertNotIn("::error", result.stdout)

    def test_update_failure_stops_install_and_is_annotated(self):
        result, calls = self.run_install("update")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(calls, ["apt-get update"])
        self.assertIn("::error title=Test dependency installation::E: update failed at 100%25", result.stdout)

    def test_install_failure_is_not_hidden(self):
        result, calls = self.run_install("install")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(len(calls), 2)
        self.assertIn("::error title=Test dependency installation::E: install failed at 100%25", result.stdout)


if __name__ == "__main__":
    unittest.main()
