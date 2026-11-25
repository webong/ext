import importlib.util
import io
import json
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("gate", pathlib.Path(__file__).with_name("check-go-test-results.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class AcceptanceEvents(unittest.TestCase):
    def check_events(self, *events):
        return gate.verify((json.dumps(dict(Package="example", **e)) for e in events), ["TestRequired"], io.StringIO())

    def test_pass_requires_test_and_package(self):
        self.assertEqual([], self.check_events(dict(Action="pass", Test="TestRequired"), dict(Action="pass")))
        self.assertTrue(self.check_events(dict(Action="pass", Test="TestRequired")))

    def test_successful_empty_selection_is_not_acceptance(self):
        self.assertTrue(self.check_events(dict(Action="pass")))

    def test_skipped_test_and_skipped_subtest_are_failures(self):
        self.assertTrue(self.check_events(dict(Action="skip", Test="TestRequired"), dict(Action="pass")))
        self.assertTrue(self.check_events(dict(Action="skip", Test="TestRequired/platform"),
                                         dict(Action="pass", Test="TestRequired"), dict(Action="pass")))

    def test_unrelated_failure_is_not_hidden(self):
        self.assertTrue(self.check_events(dict(Action="pass", Test="TestRequired"),
                                         dict(Action="fail", Test="TestOther"), dict(Action="pass")))

    def test_invalid_or_empty_stream_is_rejected(self):
        self.assertTrue(gate.verify([], ["TestRequired"], io.StringIO()))
        self.assertTrue(gate.verify(["not-json"], ["TestRequired"], io.StringIO()))

    def test_required_subtest_rejects_skipped_descendants(self):
        events = [dict(Action="skip", Test="TestGraph/cleanup/windows"),
                  dict(Action="pass", Test="TestGraph/cleanup"), dict(Action="pass")]
        failures = gate.verify(
            (json.dumps(dict(Package="example", **event)) for event in events),
            ["TestGraph/cleanup"], io.StringIO())
        self.assertTrue(any("required case skipped" in failure for failure in failures))

    def test_required_subtest_does_not_select_similarly_named_sibling(self):
        events = [dict(Action="skip", Test="TestGraph/cleanup_optional"),
                  dict(Action="pass", Test="TestGraph/cleanup"), dict(Action="pass")]
        self.assertEqual([], gate.verify(
            (json.dumps(dict(Package="example", **event)) for event in events),
            ["TestGraph/cleanup"], io.StringIO()))


if __name__ == "__main__":
    unittest.main()
