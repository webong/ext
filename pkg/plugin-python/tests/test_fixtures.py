"""The shared fixtures for progress events and reverse calls (lib/plugin/testdata/v1), which the
Go contract computes and every SDK reproduces: frames, the sender's rate limiter and the wire
model."""
import io
import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, ".."))
import ext_plugin  # noqa: E402
from ext_plugin import frames  # noqa: E402
from ext_plugin import guest as g  # noqa: E402

FIXTURES = os.path.join(HERE, "..", "..", "..", "lib", "plugin", "testdata", "v1")


def fixture(name):
    with open(os.path.join(FIXTURES, name), "rb") as f:
        return json.loads(f.read().decode("utf-8"))


def accepts(judge, *args):
    try:
        judge(*args)
        return True
    except (ext_plugin.ProtocolError, ValueError):
        return False


class Mismatches:
    """Collects the first few so a failure says what diverged."""

    def __init__(self):
        self.first = []
        self.count = 0

    def note(self, section, case, what):
        self.count += 1
        if len(self.first) < 12:
            self.first.append("%s: %s: %s" % (section, case["name"], what))


class FixtureTest(unittest.TestCase):
    def check(self, bad):
        self.assertEqual(bad.count, 0, "%d mismatches, the first:\n%s" % (bad.count, "\n".join(bad.first)))

    def test_frames_are_judged_as_the_go_contract_judges_them(self):
        root = fixture("frame-cases.json")
        bad = Mismatches()
        total = 0
        sections = (
            ("progress", lambda raw, _id: frames.validate_progress_frame(g.decode_frame(raw))),
            ("reverseRequest", lambda raw, _id: frames.validate_reverse_request(g.decode_frame(raw))),
            ("reverseResponse", lambda raw, rid: frames.validate_reverse_response(g.decode_frame(raw), rid)),
        )
        for section, judge in sections:
            cases = root[section]
            valid = sum(1 for c in cases if c["valid"])
            self.assertTrue(len(cases) >= 20 and valid >= 5 and len(cases) - valid >= 5, section + " proves little")
            for case in cases:
                total += 1
                got = accepts(judge, case["frame"].encode("utf-8"), case.get("id", ""))
                if got != case["valid"]:
                    bad.note(section, case, "accepted %s: %s" % (got, case["frame"]))
        self.assertGreaterEqual(total, 500)
        self.check(bad)

    def test_the_rate_limiter_reproduces_every_step(self):
        cases = fixture("progress-limiter-cases.json")["cases"]
        self.assertGreaterEqual(len(cases), 10)
        bad = Mismatches()
        for case in cases:
            limiter = frames.Limiter(case["eventsPerSecond"], case["bytesPerSecond"], 0)
            for i, step in enumerate(case["steps"]):
                got = limiter.admit(step["textBytes"], step["atMs"] * 1_000_000)
                want = step["admitted"]
                if (got is not None) != want or (want and got != step.get("dropped", 0)):
                    bad.note("limiter", case, "step %d: %r, want %s %s" % (i, got, want, step.get("dropped", 0)))
            if limiter.pending() != case["pending"]:
                bad.note("limiter", case, "pending count")
        self.check(bad)

    def test_the_wire_model_is_judged_as_the_go_contract_judges_it(self):
        root = fixture("model-cases.json")
        bad = Mismatches()
        self.assertGreater(len(root["descriptors"]), 100)
        for case in root["descriptors"]:
            got = True
            try:
                ext_plugin.validate_descriptor(g.decode_frame(case["descriptor"].encode("utf-8")))
            except (ext_plugin.ProtocolError, ValueError):
                got = False
            if got != case["valid"]:
                bad.note("descriptor", case, "accepted %s: %s" % (got, case["descriptor"]))
        selected = root["requestDescriptor"]
        ext_plugin.validate_descriptor(selected)
        self.assertGreater(len(root["requests"]), 100)
        for case in root["requests"]:
            ran = []
            guest = ext_plugin.Guest(selected, lambda c, o, p: ran.append(ext_plugin.current_call()) or None)
            link = frames.Link(io.BytesIO(), io.BytesIO())
            try:
                guest.invoke(g.decode_frame(case["request"].encode("utf-8")), link)
            except ext_plugin.ProtocolError:
                pass
            if bool(ran) != case["valid"]:
                bad.note("request", case, "accepted %s: %s" % (bool(ran), case["request"]))
                continue
            if not ran:
                continue
            # The call gets a reporter when its operation declared progress and the request
            # carries a rate; the rate has its defaults filled in.
            limits = ran[0].progress_limits
            if (limits is not None) != bool(case.get("reporter")):
                bad.note("request", case, "reporter %s" % (limits is not None))
            elif limits is not None and (limits.events_per_second != case["events"] or limits.bytes_per_second != case["bytes"]):
                bad.note("request", case, "rate %s" % (limits,))
        self.check(bad)


if __name__ == "__main__":
    unittest.main()
