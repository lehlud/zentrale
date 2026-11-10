import json
import unittest

from report_go_tests import annotations


class ReportTests(unittest.TestCase):
    def test_only_failures_are_reported_and_commands_are_escaped(self):
        events = [
            {"Package": "p", "Test": "TestBad", "Output": "assert failed\n::warning::not a command\r100%"},
            {"Package": "p", "Test": "TestBad", "Action": "fail"},
            {"Package": "p", "Test": "TestGood", "Action": "pass"},
        ]
        result = list(annotations(["non-json build output"] + [json.dumps(e) for e in events]))
        self.assertEqual(len(result), 1)
        self.assertIn("p / TestBad%0Aassert failed%0A::warning::not a command%0D100%25", result[0])
        self.assertNotIn("\n", result[0])

    def test_package_startup_failure_and_output_limit(self):
        result = list(annotations(map(json.dumps, [
            {"Package": "p", "Output": "x" * 20000},
            {"Package": "p", "Action": "fail"},
        ])))
        self.assertEqual(len(result), 1)
        self.assertLess(len(result[0]), 12100)

    def test_fatal_runtime_output_is_kept_without_a_test_fail_event(self):
        result = list(annotations(map(json.dumps, [
            {"Package": "p", "Test": "TestCrash", "Output": "panic: runtime failure\nstack trace\n"},
            {"Package": "p", "Output": "x" * 20000},
            {"Package": "p", "Action": "fail"},
        ])))
        self.assertEqual(len(result), 1)
        self.assertIn("panic: runtime failure%0Astack trace", result[0])
