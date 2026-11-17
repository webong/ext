package store_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

// TestWriteArchiveFixtures writes lib/plugin/testdata/v1/archive-cases.json,
// the archives it names and download-cases.json. It runs only with
// EXT_UPDATE_FIXTURES=1: compressed output differs between Go releases, so the
// committed archives are the fixtures and are not compared with a fresh build.
// TestArchiveCases and TestDownloadCases hold this store to what is committed.
func TestWriteArchiveFixtures(t *testing.T) {
	if os.Getenv("EXT_UPDATE_FIXTURES") != "1" {
		t.Skip("set EXT_UPDATE_FIXTURES=1 to rewrite the archive fixtures")
	}
	directory := filepath.Join(archiveFixtureDir, "archives")
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	digests := map[string]string{}
	put := func(name string, data []byte) string {
		if err := os.WriteFile(filepath.Join(directory, name+".bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		digests[name] = sum(data)
		return name
	}

	const id = "example/archive"
	one := fixturePackage(t, id, "r1", map[string]string{"src/main.py": "print('one')\n"})
	two := fixturePackage(t, id, "r2", map[string]string{"src/main.py": "print('two')\n", "src/extra.py": "pass\n"})
	changed := fixturePackage(t, id, "r1", map[string]string{"src/main.py": "print('changed')\n"})
	long := strings.Repeat("a", 90) + "/" + strings.Repeat("b", 90) + "/" + strings.Repeat("c", 90) + ".py"
	deep := fixturePackage(t, id, "r1", map[string]string{long: "pass\n"})
	good := members(t, one, "")
	with := func(extra ...member) []member { return append(append([]member{}, good...), extra...) }
	inDirectory := append([]member{
		{name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, pax: map[string]string{"comment": "0123456789abcdef0123456789abcdef01234567"}},
		{name: "./"}, {name: "./pkg-1/"}, {name: "./pkg-1/src/"},
	}, members(t, one, "./pkg-1/")...)
	stored := members(t, one, "")
	for i := range stored {
		stored[i].stored = true
	}
	gnu := members(t, deep, "")
	for i := range gnu {
		gnu[i].gnu = true
	}
	prefixed := strings.Repeat("d", 90) + "/" + strings.Repeat("f", 50) + ".py"
	dos := append([]member{{name: "src/", dos: true}}, good...)
	for i := range dos {
		dos[i].dos = true
	}
	big := fixturePackage(t, id, "r1", map[string]string{"src/main.py": "print('one')\n", "zeros": string(make([]byte, 1<<20))})
	whole := tarGz(t, good)
	plain := tarred(t, good)

	put("tar-gz-top", whole)
	put("tar-gz-directory", tarGz(t, inDirectory))
	put("tar-plain", plain)
	put("tar-pax-long-path", tarred(t, members(t, deep, "")))
	put("zip-top", zipped(t, good))
	put("zip-directory", zipped(t, append([]member{{name: "pkg-1/"}, {name: "pkg-1/src/"}}, members(t, one, "pkg-1/")...)))
	put("zip-stored", zipped(t, stored))
	put("tar-gz-r2", tarGz(t, members(t, two, "")))
	put("zip-r1-changed", zipped(t, members(t, changed, "")))
	put("tar-symlink", tarred(t, with(member{name: "link", link: "/tmp/ext-archive-escape"})))
	put("tar-hard-link", tarred(t, with(member{name: "link", typeflag: tar.TypeLink, link: "src/main.py"})))
	put("tar-device", tarred(t, with(member{name: "device", typeflag: tar.TypeChar})))
	put("tar-fifo", tarred(t, with(member{name: "fifo", typeflag: tar.TypeFifo})))
	put("zip-symlink", zipped(t, with(member{name: "link", link: "/tmp/ext-archive-escape"})))
	put("tar-parent-path", tarred(t, with(member{name: "../../ext-archive-escape", body: []byte("x")})))
	put("tar-gz-inner-parent-path", tarGz(t, with(member{name: "src/../../ext-archive-escape", body: []byte("x")})))
	put("zip-parent-path", zipped(t, with(member{name: "../ext-archive-escape", body: []byte("x")})))
	put("tar-absolute-path", tarred(t, with(member{name: "/tmp/ext-archive-escape", body: []byte("x")})))
	put("zip-absolute-path", zipped(t, with(member{name: "/tmp/ext-archive-escape", body: []byte("x")})))
	put("zip-backslash-path", zipped(t, with(member{name: `src\evil.py`, body: []byte("x")})))
	put("tar-device-name", tarred(t, with(member{name: "src/NUL.txt", body: []byte("x")})))
	put("tar-trailing-dot", tarred(t, with(member{name: "src/name.", body: []byte("x")})))
	put("tar-control-character", tarred(t, with(member{name: "src/a\x01b", body: []byte("x")})))
	put("tar-case-duplicate", tarred(t, with(member{name: "SRC/MAIN.PY", body: []byte("x")})))
	put("zip-exact-duplicate", zipped(t, with(good[len(good)-1])))
	put("tar-file-over-directory", tarred(t, with(member{name: "src", body: []byte("x")})))
	put("zip-two-directories", zipped(t, append(members(t, one, "a/"), member{name: "b/x", body: []byte("x")})))
	put("tar-no-manifest", tarred(t, []member{{name: "src/main.py", body: []byte("pass\n")}}))
	put("tar-manifest-too-deep", tarred(t, members(t, one, "a/b/")))
	put("tar-unlisted-file", tarred(t, with(member{name: "extra", body: []byte("x")})))
	put("unknown-format", []byte("Rar!\x1a\x07\x00 is not a format this store reads"))
	put("empty", nil)
	put("gzip-not-tar", gzipped(t, []byte("a gzip stream that holds no tar archive")))
	put("tar-gz-truncated", whole[:len(whole)/2])
	put("tar-truncated", plain[:len(plain)-1024-512+5])
	put("tar-without-trailer", plain[:len(plain)-1024])
	put("tar-entry-under-file", tarred(t, with(member{name: "src/main.py/inner", body: []byte("x")})))
	put("zip-truncated", zipped(t, good)[:200])
	put("zip-size-understated", rawZip(t, good, func(h *zip.FileHeader) { h.UncompressedSize64 = 4 }))
	put("zip-size-overstated", rawZip(t, good, func(h *zip.FileHeader) { h.UncompressedSize64 += 100 }))
	put("zip-bad-crc", rawZip(t, good, func(h *zip.FileHeader) { h.CRC32 ^= 1 }))
	put("zip-unknown-method", rawZip(t, good, func(h *zip.FileHeader) { h.Method = 12 }))
	put("zip-expands", zipped(t, members(t, big, "")))
	put("tar-gz-expands", tarGz(t, members(t, big, "")))
	put("tar-gz-header-fields", gzipNamed(t, plain))
	put("tar-gnu-long-name", tarred(t, gnu))
	put("tar-ustar-prefix", tarred(t, members(t, fixturePackage(t, id, "r1", map[string]string{prefixed: "pass\n"}), "")))
	put("zip-dos-attributes", zipped(t, dos))
	put("tar-cut-in-header", cutInHeader(t, plain))
	put("tar-gz-cut-in-header", gzipped(t, cutInHeader(t, plain)))
	put("zip-zip64", zip64(t, zipped(t, good)))
	put("tar-invalid-utf8", tarred(t, with(member{name: "src/\xff\xfe.py", body: []byte("x")})))
	put("zip-invalid-utf8", zipped(t, with(member{name: "src/\xc3.py", body: []byte("x")})))

	text := func(value string) *string { return &value }
	install := func(name, status, revision string) archiveStep {
		return archiveStep{Install: "archives/" + name + ".bin", SHA256: text(digests[name]), Status: status, Revision: revision}
	}
	receipt := func(name, revision string) archiveStep {
		return archiveStep{Receipt: id, SHA256: text(digests[name]), Revision: revision}
	}
	single := func(name, status, revision string) archiveCase {
		return archiveCase{Name: name, Steps: []archiveStep{install(name, status, revision)}}
	}
	limited := func(name, archive string, limits caseLimits) archiveCase {
		return archiveCase{Name: name, Limits: &limits, Steps: []archiveStep{install(archive, "invalid", "")}}
	}
	changedInput := func(name string, change func(*archiveStep)) archiveCase {
		step := install("tar-gz-top", "", "")
		change(&step)
		return archiveCase{Name: name, Steps: []archiveStep{step}}
	}
	fixture := archiveCases{About: archiveAbout}
	for _, name := range []string{"tar-gz-top", "tar-gz-directory", "tar-plain", "tar-pax-long-path", "tar-without-trailer", "tar-gz-header-fields", "tar-gnu-long-name", "tar-ustar-prefix", "zip-top", "zip-directory", "zip-stored", "zip-dos-attributes", "zip-expands", "tar-gz-expands"} {
		fixture.Cases = append(fixture.Cases, archiveCase{Name: name, Steps: []archiveStep{install(name, "", "r1"), receipt(name, "r1")}})
	}
	fixture.Cases = append(fixture.Cases,
		archiveCase{Name: "upgrade-then-remove", Steps: []archiveStep{
			install("zip-top", "", "r1"), install("tar-gz-r2", "", "r2"), receipt("tar-gz-r2", "r2"),
			{Remove: id}, {Receipt: id, Status: "not_found"}, {Remove: id, Status: "not_found"},
		}},
		archiveCase{Name: "expected-id", Steps: []archiveStep{
			{Install: "archives/tar-plain.bin", SHA256: text(digests["tar-plain"]), ID: id, Revision: "r1"},
		}},
		archiveCase{Name: "receipt-skips-the-archive", Steps: []archiveStep{
			install("tar-gz-top", "", "r1"),
			{Install: "archives/absent.bin", SHA256: text(digests["tar-gz-top"]), Revision: "r1"},
			{Install: "archives/absent.bin", SHA256: text(strings.ToUpper(digests["tar-gz-top"])), ID: id, Revision: "r1"},
		}},
		archiveCase{Name: "receipt-does-not-vouch-for-damage", Steps: []archiveStep{
			install("tar-gz-top", "", "r1"),
			{Damage: id, Path: "src/main.py"},
			install("tar-gz-top", "mismatch", ""),
			receipt("tar-gz-top", "r1"),
		}},
		archiveCase{Name: "same-revision-same-content", Steps: []archiveStep{
			install("tar-gz-top", "", "r1"), install("zip-top", "", "r1"), receipt("zip-top", "r1"),
			{Install: "archives/absent.bin", SHA256: text(digests["zip-top"]), Revision: "r1"},
		}},
		archiveCase{Name: "same-revision-other-content", Steps: []archiveStep{
			install("tar-gz-top", "", "r1"), install("zip-r1-changed", "mismatch", ""), receipt("tar-gz-top", "r1"),
		}},
		changedInput("hash-mismatch", func(s *archiveStep) {
			s.SHA256, s.Status = text(digests["zip-top"]), "mismatch"
		}),
		changedInput("hash-missing", func(s *archiveStep) { s.SHA256, s.Status = text(""), "invalid" }),
		changedInput("hash-malformed", func(s *archiveStep) { s.SHA256, s.Status = text("sha256:"+digests["tar-gz-top"][:57]), "invalid" }),
		changedInput("another-package-expected", func(s *archiveStep) { s.ID, s.Status = "example/other", "mismatch" }),
		changedInput("invalid-expected-id", func(s *archiveStep) { s.ID, s.Status = "not an id", "invalid" }),
		limited("archive-over-limit", "tar-gz-top", caseLimits{MaxArchiveBytes: 64}),
		limited("tar-total-over-limit", "tar-gz-top", caseLimits{MaxTreeBytes: 16}),
		limited("zip-total-over-limit", "zip-top", caseLimits{MaxTreeBytes: 16}),
		limited("tar-file-over-limit", "tar-plain", caseLimits{MaxArtifactBytes: 4}),
		limited("zip-file-over-limit", "zip-stored", caseLimits{MaxArtifactBytes: 4}),
		limited("tar-entries-over-limit", "tar-gz-directory", caseLimits{MaxTreeFiles: 3}),
		limited("zip-entries-over-limit", "zip-directory", caseLimits{MaxTreeFiles: 3}),
		limited("zip-expands-over-limit", "zip-expands", caseLimits{MaxTreeBytes: 4096}),
		limited("tar-gz-expands-over-limit", "tar-gz-expands", caseLimits{MaxTreeBytes: 4096}),
	)
	for _, name := range []string{
		"tar-symlink", "tar-hard-link", "tar-device", "tar-fifo", "zip-symlink",
		"tar-parent-path", "tar-gz-inner-parent-path", "zip-parent-path", "tar-absolute-path", "zip-absolute-path",
		"zip-backslash-path", "tar-device-name", "tar-trailing-dot", "tar-control-character", "tar-invalid-utf8", "zip-invalid-utf8",
		"tar-case-duplicate", "zip-exact-duplicate", "tar-file-over-directory", "tar-entry-under-file",
		"zip-two-directories", "tar-no-manifest", "tar-manifest-too-deep", "tar-unlisted-file",
		"gzip-not-tar", "tar-gz-truncated", "tar-truncated", "zip-truncated",
		"tar-cut-in-header", "tar-gz-cut-in-header", "zip-zip64",
		"zip-size-understated", "zip-size-overstated", "zip-bad-crc", "zip-unknown-method",
	} {
		fixture.Cases = append(fixture.Cases, single(name, "invalid", ""))
	}
	fixture.Cases = append(fixture.Cases, single("unknown-format", "unsupported", ""), single("empty", "unsupported", ""))
	writeFixture(t, "archive-cases.json", fixture)
	writeFixture(t, "download-cases.json", downloadFixture())
}

const archiveAbout = "What a package store in any language must do with a pinned archive. Each case starts with an empty store under the case's limits (maxArchiveBytes, maxTreeFiles, maxTreeBytes, maxArtifactBytes; a limit that is absent is the store's default) and runs its steps in order. " +
	"An 'install' step installs or upgrades from the archive file it names, relative to this file, with the pinned 'sha256' (hexadecimal in either case; an empty string is a missing digest) and, when 'id' is present, the package ID the archive must hold; a file that does not exist ('archives/absent.bin') must never be opened, which is how a case shows that a receipt made the store skip the archive. " +
	"A 'receipt' step reads the install receipt of a package and expects its 'revision' and, as 'sha256', the lower-case digest of the archive that produced it. A 'remove' step removes a package. A 'damage' step overwrites the file 'path' of the installed package, in the directory named by its ID with every '/' written '%2f', with the seven bytes 'damaged'. " +
	"Every step expects 'status' (ok when absent): ok, invalid, mismatch, unsupported, not_found or exists. A successful install also expects the installed package's 'revision'. " +
	"The format is found from the content: a zip starts 'PK\\x03\\x04' or 'PK\\x05\\x06', a gzip-compressed tar starts 0x1f 0x8b, a plain tar has 'ustar' at offset 257; anything else is unsupported. Only directories and regular files are extracted; a tar global header is skipped. " +
	"The manifest, plugin.json, is at the top of the archive or in its one top-level directory. Each entry, directory or file, counts toward maxTreeFiles; the bytes of files toward maxTreeBytes; one file toward maxArtifactBytes; the archive file toward maxArchiveBytes. " +
	"After every case nothing is left under the store root but package directories and '.receipts', and no case may create /tmp/ext-archive-escape."

func writeFixture(t *testing.T, name string, value any) {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archiveFixtureDir, name), buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixturePackage builds a source-only package directory holding files.
func fixturePackage(t *testing.T, id, revision string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := packagekit.Manifest{APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: id, Revision: revision},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.read", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}}}
	names := make([]string, 0, len(files))
	for path := range files {
		names = append(names, path)
	}
	sortStrings(names)
	for i, path := range names {
		manifest.Artifacts = append(manifest.Artifacts, packagekit.Artifact{Name: "file" + itoa(i), Path: path, SHA256: write(t, dir, path, files[path])})
	}
	if err := store.WriteManifest(dir, manifest); err != nil {
		t.Fatal(err)
	}
	return dir
}

func sortStrings(values []string) {
	for i := range values {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// cutInHeader ends a tar sixteen bytes into its second header block.
func cutInHeader(t *testing.T, archive []byte) []byte {
	t.Helper()
	for at := 512; at+512 <= len(archive); at += 512 {
		if string(archive[at+257:at+262]) == "ustar" {
			return archive[:at+16]
		}
	}
	t.Fatal("no second header")
	return nil
}

// zip64 rewrites the last entry of a zip the way a zip64 writer records it: the
// sizes in the central directory give way to an extra field. The result is a
// valid archive that archive/zip reads, which is checked here; the stores
// refuse the form.
func zip64(t *testing.T, archive []byte) []byte {
	t.Helper()
	le := binary.LittleEndian
	end := len(archive) - 22
	if le.Uint32(archive[end:]) != 0x06054b50 {
		t.Fatal("no end record")
	}
	size, offset := int(le.Uint32(archive[end+12:])), int(le.Uint32(archive[end+16:]))
	last := offset
	for at := offset; at < offset+size; {
		last = at
		at += 46 + int(le.Uint16(archive[at+28:])) + int(le.Uint16(archive[at+30:])) + int(le.Uint16(archive[at+32:]))
	}
	nameLen, extraLen := int(le.Uint16(archive[last+28:])), int(le.Uint16(archive[last+30:]))
	entry := append([]byte{}, archive[last:last+46+nameLen+extraLen]...)
	extra := make([]byte, 20)
	le.PutUint16(extra[0:], 1)
	le.PutUint16(extra[2:], 16)
	le.PutUint64(extra[4:], uint64(le.Uint32(entry[24:])))
	le.PutUint64(extra[12:], uint64(le.Uint32(entry[20:])))
	le.PutUint32(entry[20:], 0xffffffff)
	le.PutUint32(entry[24:], 0xffffffff)
	le.PutUint16(entry[30:], uint16(extraLen+20))
	out := append([]byte{}, archive[:last]...)
	out = append(out, entry...)
	out = append(out, extra...)
	out = append(out, archive[last+46+nameLen+extraLen:end]...)
	tail := append([]byte{}, archive[end:]...)
	le.PutUint32(tail[12:], uint32(size+20))
	out = append(out, tail...)
	reader, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		body, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, body); err != nil {
			t.Fatalf("%s: %v", file.Name, err)
		}
	}
	if final := reader.File[len(reader.File)-1]; final.UncompressedSize != 0xffffffff || final.UncompressedSize64 == 0xffffffff {
		t.Fatal("the last entry is not in the zip64 form")
	}
	return out
}

// gzipNamed compresses data with every optional field of the gzip header set.
func gzipNamed(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	writer.Name, writer.Comment, writer.Extra = "package.tar", "a comment", []byte{1, 2, 3, 4}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// rawZip writes entries with correct deflate bodies, then lets change falsify
// the header of the last one.
func rawZip(t *testing.T, entries []member, change func(*zip.FileHeader)) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for i, e := range entries {
		var body bytes.Buffer
		deflate, err := flate.NewWriter(&body, flate.DefaultCompression)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := deflate.Write(e.body); err != nil {
			t.Fatal(err)
		}
		if err := deflate.Close(); err != nil {
			t.Fatal(err)
		}
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(e.body),
			CompressedSize64: uint64(body.Len()), UncompressedSize64: uint64(len(e.body))}
		header.SetMode(0o644)
		if i == len(entries)-1 {
			change(header)
		}
		out, err := writer.CreateRaw(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(body.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func downloadFixture() downloadCases {
	secrets := map[string]string{"Authorization": "Bearer secret", "Cookie": "session=secret", "Referer": "https://private.example/page"}
	absent := []string{"Authorization", "Cookie", "Referer"}
	return downloadCases{
		About: "What a download of a package archive must do in any language. A test serves 'routes' from an HTTPS origin it trusts and runs a plain HTTP server beside it that must never receive a request. A route answers with 'status' (200 when absent) and 'body', or 'bodyBytes' bytes of 'x'; with a Content-Length unless 'chunked'; or with a 302 to 'redirect'. '{origin}' and '{plain}' in a redirect or a case's 'url' stand for the host and port of the two servers. " +
			"A case downloads 'url' into a new file with the limit 'maxBytes' (the default limit when absent), sending 'headers' with the first request when it names any, and into a path that already holds a file when 'destinationExists'. 'expect' is ok, with the file holding 'body'; invalid, a refusal by the download rules; or failed, any other failure. A download that is not ok leaves no file, and never changes one that existed. " +
			"The rules: only https URLs with a host and without user information or a fragment, for the first request and for every redirect; at most 'maxRedirects' redirects; the headers in 'absentAtLanding' are not sent with the last request the origin received; the body is no larger than the limit, whatever Content-Length says.",
		MaxRedirects: store.MaxDownloadRedirects,
		Routes: map[string]downloadRoute{
			"/payload":       {Body: "payload"},
			"/start":         {Redirect: "/payload"},
			"/absolute":      {Redirect: "https://{origin}/payload"},
			"/hop/1":         {Redirect: "/payload"},
			"/hop/2":         {Redirect: "/hop/1"},
			"/hop/3":         {Redirect: "/hop/2"},
			"/hop/4":         {Redirect: "/hop/3"},
			"/hop/5":         {Redirect: "/hop/4"},
			"/hop/6":         {Redirect: "/hop/5"},
			"/loop":          {Redirect: "/loop"},
			"/downgrade":     {Redirect: "http://{plain}/payload"},
			"/to-user":       {Redirect: "https://user:secret@{origin}/payload"},
			"/to-fragment":   {Redirect: "https://{origin}/payload#part"},
			"/missing":       {Status: 404, Body: "no such archive"},
			"/server-error":  {Status: 500, Body: "payload"},
			"/large":         {BodyBytes: 4096},
			"/large-chunked": {BodyBytes: 4096, Chunked: true},
		},
		Cases: []downloadCase{
			{Name: "direct", URL: "https://{origin}/payload", Expect: "ok", Body: "payload"},
			{Name: "exactly-at-the-limit", URL: "https://{origin}/payload", MaxBytes: 7, Expect: "ok", Body: "payload"},
			{Name: "one-byte-over-the-limit", URL: "https://{origin}/payload", MaxBytes: 6, Expect: "invalid"},
			{Name: "redirect", URL: "https://{origin}/start", Expect: "ok", Body: "payload"},
			{Name: "absolute-redirect", URL: "https://{origin}/absolute", Expect: "ok", Body: "payload"},
			{Name: "credentials-do-not-follow-a-redirect", URL: "https://{origin}/start", Headers: secrets, Expect: "ok", Body: "payload", AbsentAtLanding: absent},
			{Name: "five-redirects", URL: "https://{origin}/hop/5", Expect: "ok", Body: "payload"},
			{Name: "six-redirects", URL: "https://{origin}/hop/6", Expect: "invalid"},
			{Name: "redirect-loop", URL: "https://{origin}/loop", Expect: "invalid"},
			{Name: "redirect-to-http", URL: "https://{origin}/downgrade", Expect: "invalid"},
			{Name: "redirect-to-user-information", URL: "https://{origin}/to-user", Expect: "invalid"},
			{Name: "redirect-to-fragment", URL: "https://{origin}/to-fragment", Expect: "invalid"},
			{Name: "not-found", URL: "https://{origin}/missing", Expect: "failed"},
			{Name: "server-error", URL: "https://{origin}/server-error", Expect: "failed"},
			{Name: "declared-length-over-the-limit", URL: "https://{origin}/large", MaxBytes: 1024, Expect: "invalid"},
			{Name: "streamed-body-over-the-limit", URL: "https://{origin}/large-chunked", MaxBytes: 1024, Expect: "invalid"},
			{Name: "http-url", URL: "http://{plain}/payload", Expect: "invalid"},
			{Name: "user-information", URL: "https://user:secret@{origin}/payload", Expect: "invalid"},
			{Name: "fragment", URL: "https://{origin}/payload#part", Expect: "invalid"},
			{Name: "file-url", URL: "file:///etc/hosts", Expect: "invalid"},
			{Name: "no-host", URL: "https:///payload", Expect: "invalid"},
			{Name: "existing-destination", URL: "https://{origin}/payload", DestinationExists: true, Expect: "failed"},
		},
	}
}
