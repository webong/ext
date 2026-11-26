#!/usr/bin/env python3
"""Run the shared network option and address cases against the Linux C ABI."""

import ctypes
import json
import sys
from pathlib import Path


class Options(ctypes.Structure):
    _fields_ = [
        ("max_interfaces", ctypes.c_uint32),
        ("max_addresses", ctypes.c_uint32),
        ("max_routes", ctypes.c_uint32),
        ("timeout_ms", ctypes.c_uint32),
    ]


class Address(ctypes.Structure):
    _fields_ = [
        ("ip", ctypes.c_char * 46),
        ("prefix_length", ctypes.c_uint8),
        ("family", ctypes.c_uint8),
        ("scope", ctypes.c_char * 16),
    ]


library = ctypes.CDLL(sys.argv[1])
library.ext_system_net_validate_options.argtypes = [Options]
library.ext_system_net_validate_options.restype = ctypes.c_int
library.ext_system_net_parse_address.argtypes = [
    ctypes.c_char_p, ctypes.c_int, ctypes.POINTER(Address)
]
library.ext_system_net_parse_address.restype = ctypes.c_int
cases = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))

for case in cases["options"]:
    values = [case["maxInterfaces"], case["maxAddresses"], case["maxRoutes"]]
    actual = library.ext_system_net_validate_options(Options(*values, 5000))
    assert (actual != 0) == (case["code"] == "invalid"), case["name"]

for case in cases["addresses"]:
    result = Address()
    actual = library.ext_system_net_parse_address(
        case["ip"].encode(), case["prefix"], ctypes.byref(result)
    )
    assert (actual != 0) == (case["code"] == "invalid"), case["name"]
    if actual == 0:
        want = case["want"]
        assert result.ip.decode() == want["ip"], case["name"]
        assert result.prefix_length == want["prefixLength"], case["name"]
        assert result.family == (4 if want["family"] == "ipv4" else 6), case["name"]
        assert result.scope.decode() == want["scope"], case["name"]

print("ok shared network option and address fixtures")
