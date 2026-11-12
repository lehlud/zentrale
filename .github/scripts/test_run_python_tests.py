import contextlib
import io
import subprocess
import unittest
from unittest.mock import patch

from run_python_tests import failure_annotation, main


class PythonReportTests(unittest.TestCase):
    def test_annotation_is_bounded_and_escapes_command_injection(self):
        annotation = failure_annotation("x" * 20000 + "\n::warning::data\r100%")
        self.assertIn("%0A::warning::data%0D100%25", annotation)
        self.assertNotIn("\n", annotation)
        self.assertNotIn("\r", annotation)
        self.assertLess(len(annotation), 12100)

    def test_preserves_cli_failure_exit_and_traceback(self):
        with patch("run_python_tests.subprocess.run") as run, contextlib.redirect_stdout(io.StringIO()) as output:
            run.return_value = subprocess.CompletedProcess([], 1, "FAIL: test_example\nAssertionError\n")
            self.assertEqual(main(["discover", "-s", "example", "-p", "test_*.py"]), 1)
        self.assertEqual(run.call_args.args[0][1:], ["-m", "unittest", "discover", "-s", "example", "-p", "test_*.py"])
        self.assertIn("::error title=Python test failure::FAIL: test_example%0AAssertionError", output.getvalue())

    def test_success_has_no_error_annotation(self):
        with patch("run_python_tests.subprocess.run") as run, contextlib.redirect_stdout(io.StringIO()) as output:
            run.return_value = subprocess.CompletedProcess([], 0, "OK\n")
            self.assertEqual(main([]), 0)
        self.assertEqual(output.getvalue(), "OK\n")

    def test_timeout_fails_closed(self):
        with patch("run_python_tests.subprocess.run", side_effect=subprocess.TimeoutExpired("unittest", 300)), contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(main([]), 1)
        self.assertIn("::error title=Python test failure::", output.getvalue())


if __name__ == "__main__":
    unittest.main()
