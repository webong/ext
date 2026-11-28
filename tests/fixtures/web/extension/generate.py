"""Regenerate small, deterministic cross-language extension artifact fixtures."""
import hashlib
import io
import json
from pathlib import Path
import struct
import zipfile

root = Path(__file__).parent
manifest = b'{"manifest_version":3,"name":"Example","version":"1.0","key":"dGVzdA==","permissions":["tabs","storage","tabs",""],"host_permissions":["https://example.test/*"]}'
files = {"manifest.json": manifest, "scripts/main.js": b"console.log('ready');\n", "empty": b"", "caf\u00e9.txt": b"unicode\n"}
h = hashlib.sha256()
for name, data in sorted(files.items()):
    h.update(name.encode() + b"\0" + struct.pack(">Q", len(data)) + data)
key_hash = hashlib.sha256(b"test").digest()[:16]
expected = dict(name="Example", version="1.0", manifestVersion=3,
                id="".join(chr(97 + n) for b in key_hash for n in [b >> 4, b & 15]),
                permissions=["storage", "tabs"], hostPermissions=["https://example.test/*"],
                files=len(files), bytes=sum(map(len, files.values())), revision="sha256:" + h.hexdigest())
(root / "expected.json").write_text(json.dumps(expected, indent=2) + "\n")
class UTF8ZipInfo(zipfile.ZipInfo):
    def _encodeFilenameFlags(self):
        return self.filename.encode("utf-8"), self.flag_bits | 0x800

cases = []
def archive(name, entries, valid=False, method=zipfile.ZIP_DEFLATED, mode=None, mutate=None, mobile=False, utf8=False):
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, "w") as z:
        for path, data in entries:
            item = (UTF8ZipInfo if utf8 else zipfile.ZipInfo)(path, (1980, 1, 1, 0, 0, 0))
            item.compress_type = method
            item.create_system = 3
            item.external_attr = (mode or 0o100644) << 16
            z.writestr(item, data)
    data = bytearray(stream.getvalue())
    if mutate:
        mutate(data, data.index(b"PK\x01\x02"))
    (root / name).write_bytes(data)
    cases.append(dict(file=name, valid=valid, mobileOnly=mobile))
archive("stored.zip", files.items(), True, zipfile.ZIP_STORED)
archive("mobile.zip", sorted(files.items()), True, zipfile.ZIP_STORED, utf8=True)
archive("deflated.XPI", files.items(), True)
archive("wrapped.zip", [("wrapper/" + k, v) for k, v in files.items()], True)
for name in ["../escape", "/absolute", "a/../../escape", "a\\b", "a:b", "a//b", "./b"]:
    archive("unsafe-%d.zip" % len(cases), [("manifest.json", manifest), (name, b"x")])
archive("symlink.zip", files.items(), mode=0o120777)
archive("special.zip", files.items(), mode=0o010644)
archive("duplicate.zip", [("manifest.json", manifest), ("manifest.json", manifest)])
archive("missing.zip", [("script.js", b"x")])
archive("bad-manifest.zip", [("manifest.json", b'{"manifest_version":4,"name":"bad","version":"1"}')])
archive("bad-permissions.zip", [("manifest.json", b'{"manifest_version":3,"name":"bad","version":"1","permissions":[5]}')])
archive("bad-key.zip", [("manifest.json", b'{"manifest_version":3,"name":"bad","version":"1","key":"YQ"}')])
archive("encrypted.zip", files.items(), mutate=lambda b,c: (struct.pack_into("<H",b,6,1),struct.pack_into("<H",b,c+8,1)))
archive("oversize.zip", files.items(), mutate=lambda b,c: struct.pack_into("<I",b,c+24,17<<20))
archive("bad-crc.zip", files.items(), mutate=lambda b,c: struct.pack_into("<I",b,c+16,1))
archive("truncated.zip", files.items(), mutate=lambda b,c: b.__delitem__(slice(-8,None)))
archive("local-name.zip", files.items(), mutate=lambda b,c: b.__setitem__(30,ord("X")), mobile=True)
archive("collision.zip", list(files.items())+[("scripts",b"x")], mobile=True)
# Valid ZIP syntax but a filename the target filesystems cannot create.
archive("long-name.zip", [("manifest.json", manifest), ("z" * 300, b"x")])
cases.pop()  # Used by the stage cleanup tests, not the invalid-source corpus.
(root / "cases.json").write_text(json.dumps(cases, indent=2) + "\n")
