"""Guest used by the Go conformance suite (pkg/plugin/plugintest)."""
import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
import ext_plugin  # noqa: E402


def handler(contract, operation, payload):
    if operation == "wait":
        time.sleep(60)
    if operation == "private-error":
        raise RuntimeError("secret diagnostic")
    if operation == "public-error":
        raise ext_plugin.RemoteError("busy", "try later", retry_after_ms=10)
    return payload


if __name__ == "__main__":
    here = os.path.dirname(os.path.abspath(__file__))
    ext_plugin.serve_stdio(os.path.join(here, "descriptor.json"), handler, redact_errors=True)
