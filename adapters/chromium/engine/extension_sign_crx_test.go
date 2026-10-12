package chromium

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/extension"
)

// This file builds genuine CRX3 artifacts in the test, so signing and
// inspection are exercised without a browser. Keys and archives are generated
// per test; nothing is stored in the repository.

func protoBytes(field uint64, data []byte) []byte {
	out := binaryUvarint(field<<3 | 2)
	out = append(out, binaryUvarint(uint64(len(data)))...)
	return append(out, data...)
}

func binaryUvarint(v uint64) []byte {
	var buf [10]byte
	return buf[:binary.PutUvarint(buf[:], v)]
}

func le32(v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b[:]
}

type crxOptions struct {
	identityKey   crypto.Signer // identity derived from this key instead of the signing key
	noProof       bool
	version       uint32
	headerLength  *uint32
	magic         string
	tamperPayload bool
}

func newKey(t testing.TB, algorithm string) crypto.Signer {
	t.Helper()
	switch algorithm {
	case "rsa":
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return key
	default:
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
}

func publicDER(t testing.TB, key crypto.Signer) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// extensionIDFor returns the Chrome extension ID derived from a public key.
func extensionIDFor(t testing.TB, key crypto.Signer) string {
	t.Helper()
	sum := sha256.Sum256(publicDER(t, key))
	var id strings.Builder
	for _, value := range sum[:16] {
		id.WriteByte('a' + (value >> 4))
		id.WriteByte('a' + (value & 15))
	}
	return id.String()
}

func buildCRX(t testing.TB, archive []byte, key crypto.Signer, options crxOptions) []byte {
	t.Helper()
	identity := key
	if options.identityKey != nil {
		identity = options.identityKey
	}
	idSum := sha256.Sum256(publicDER(t, identity))
	signedHeader := protoBytes(1, idSum[:16])
	hash := sha256.New()
	hash.Write([]byte("CRX3 SignedData\x00"))
	hash.Write(le32(uint32(len(signedHeader))))
	hash.Write(signedHeader)
	hash.Write(archive)
	digest := hash.Sum(nil)
	var header []byte
	if !options.noProof {
		var signature []byte
		var field uint64
		var err error
		switch typed := key.(type) {
		case *rsa.PrivateKey:
			field = 2
			signature, err = rsa.SignPKCS1v15(rand.Reader, typed, crypto.SHA256, digest)
		case *ecdsa.PrivateKey:
			field = 3
			signature, err = ecdsa.SignASN1(rand.Reader, typed, digest)
		}
		if err != nil {
			t.Fatal(err)
		}
		proof := append(protoBytes(1, publicDER(t, key)), protoBytes(2, signature)...)
		header = protoBytes(field, proof)
	}
	header = append(header, protoBytes(10000, signedHeader)...)
	payload := archive
	if options.tamperPayload {
		payload = append([]byte(nil), archive...)
		payload[len(payload)/2] ^= 0xff
	}
	version := options.version
	if version == 0 {
		version = 3
	}
	magic := options.magic
	if magic == "" {
		magic = "Cr24"
	}
	length := uint32(len(header))
	if options.headerLength != nil {
		length = *options.headerLength
	}
	out := append([]byte(magic), le32(version)...)
	out = append(out, le32(length)...)
	out = append(out, header...)
	return append(out, payload...)
}

// zipDirectory packs a directory with manifest.json at the archive root.
func zipDirectory(t testing.TB, root string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := writer.Create(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeKeyPEM(path string, key crypto.Signer) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

func readKeyPEM(t testing.TB, path string) crypto.Signer {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("key file is not PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return key.(crypto.Signer)
}

// TestFakeCRXSigner stands in for the browser's --pack-extension command. It
// runs only as a helper process, launched through the wrapper script below.
func TestFakeCRXSigner(t *testing.T) {
	if os.Getenv("EXT_FAKE_SIGNER") != "1" {
		t.Skip("helper process")
	}
	var directory, keyFile string
	for i, argument := range os.Args {
		if argument != "--" {
			continue
		}
		for _, value := range os.Args[i+1:] {
			if rest, ok := strings.CutPrefix(value, "--pack-extension="); ok {
				directory = rest
			}
			if rest, ok := strings.CutPrefix(value, "--pack-extension-key="); ok {
				keyFile = rest
			}
		}
	}
	if directory == "" {
		t.Fatal("no --pack-extension argument")
	}
	algorithm := os.Getenv("EXT_FAKE_SIGNER_ALG")
	var key crypto.Signer
	if keyFile != "" {
		key = readKeyPEM(t, keyFile)
	} else {
		key = newKey(t, algorithm)
		if err := writeKeyPEM(directory+".pem", key); err != nil {
			t.Fatal(err)
		}
	}
	options := crxOptions{}
	switch os.Getenv("EXT_FAKE_SIGNER_MODE") {
	case "wrong-key":
		options.identityKey = newKey(t, algorithm)
	case "tamper":
		options.tamperPayload = true
	case "truncate":
		huge := uint32(1 << 19)
		options.headerLength = &huge
	case "bad-magic":
		options.magic = "Cr25"
	case "version-2":
		options.version = 2
	case "no-proof":
		options.noProof = true
	case "mutate-source":
		source := filepath.Join(os.Getenv("EXT_FAKE_SIGNER_SOURCE"), "background.js")
		file, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		file.WriteString("// changed during packaging\n")
		file.Close()
	}
	if err := os.WriteFile(directory+".crx", buildCRX(t, zipDirectory(t, directory), key, options), 0o600); err != nil {
		t.Fatal(err)
	}
}

// crxSigner writes the wrapper script SignChromiumCRX launches as "the browser".
func crxSigner(t *testing.T, mode, algorithm, source string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXT_FAKE_SIGNER", "1")
	t.Setenv("EXT_FAKE_SIGNER_MODE", mode)
	t.Setenv("EXT_FAKE_SIGNER_ALG", algorithm)
	t.Setenv("EXT_FAKE_SIGNER_SOURCE", source)
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "'"
	return writeSigner(t, "browser", "exec "+quoted+" -test.run='^TestFakeCRXSigner$' -- \"$@\"\n")
}

func TestSignChromiumCRXPublishesAGenuineCRX3(t *testing.T) {
	for _, algorithm := range []string{"ecdsa", "rsa"} {
		t.Run(algorithm, func(t *testing.T) {
			source := extensionFixture(t)
			prepared, err := extension.Inspect(source)
			if err != nil {
				t.Fatal(err)
			}
			browser := crxSigner(t, "", algorithm, source)
			output := filepath.Join(t.TempDir(), "built.crx")
			result, err := SignChromiumCRX(context.Background(), "example", browser, source, output, prepared.Revision, "")
			if err != nil {
				t.Fatal(err)
			}
			keyPath := strings.TrimSuffix(output, ".crx") + ".pem"
			key := readKeyPEM(t, keyPath)
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			if result.Status != "signed" || result.KeyPath != keyPath || result.Source != output ||
				result.ID != extensionIDFor(t, key) || result.Version != "1.0" ||
				result.SourceRevision != prepared.Revision || result.ArtifactRevision != "sha256:"+hex.EncodeToString(digest[:]) {
				t.Fatalf("incorrect result: %+v", result)
			}
			if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("private key mode: %v %v", info, err)
			}
			if info, err := os.Stat(output); err != nil || info.Mode().Perm() != 0o644 {
				t.Fatalf("artifact mode: %v %v", info, err)
			}
			inspected, err := InspectCRX(output)
			if err != nil || inspected.ID != result.ID || inspected.Revision != result.ArtifactRevision {
				t.Fatalf("published artifact does not inspect cleanly: %+v %v", inspected, err)
			}
			if current, err := extension.Inspect(source); err != nil || current.Revision != prepared.Revision {
				t.Fatalf("source changed: %+v %v", current, err)
			}
			if _, err := SignChromiumCRX(context.Background(), "example", browser, source, output, prepared.Revision, ""); err == nil {
				t.Fatal("overwrote a published CRX")
			}
		})
	}
}

func TestSignChromiumCRXReusesAnExistingKey(t *testing.T) {
	source := extensionFixture(t)
	prepared, _ := extension.Inspect(source)
	key := newKey(t, "ecdsa")
	keyPath := filepath.Join(t.TempDir(), "developer.pem")
	if err := writeKeyPEM(keyPath, key); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(keyPath)
	browser := crxSigner(t, "", "ecdsa", source)
	output := filepath.Join(t.TempDir(), "built.crx")
	result, err := SignChromiumCRX(context.Background(), "example", browser, source, output, prepared.Revision, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != extensionIDFor(t, key) || result.KeyPath != keyPath {
		t.Fatalf("an existing key must give a stable extension ID: %+v", result)
	}
	after, _ := os.ReadFile(keyPath)
	if !bytes.Equal(before, after) {
		t.Fatal("existing key was modified")
	}
	if _, err := os.Stat(strings.TrimSuffix(output, ".crx") + ".pem"); !os.IsNotExist(err) {
		t.Fatalf("a second key was written beside the output: %v", err)
	}
}

func TestSignChromiumCRXRejectsBadSignerOutput(t *testing.T) {
	reasons := map[string]string{
		"wrong-key":     "does not match its extension identity",
		"tamper":        "signature verification failed",
		"truncate":      "invalid CRX3 header length",
		"bad-magic":     "complete CRX3 artifact",
		"version-2":     "complete CRX3 artifact",
		"no-proof":      "bounded signing proofs",
		"mutate-source": "changed during CRX packaging",
	}
	for mode, reason := range reasons {
		t.Run(mode, func(t *testing.T) {
			source := extensionFixture(t)
			prepared, _ := extension.Inspect(source)
			browser := crxSigner(t, mode, "ecdsa", source)
			dir := t.TempDir()
			output := filepath.Join(dir, "built.crx")
			_, err := SignChromiumCRX(context.Background(), "example", browser, source, output, prepared.Revision, "")
			if err == nil {
				t.Fatal("accepted")
			}
			// The failure must come from validating the artifact, not from the
			// fake signer itself failing to run.
			if strings.Contains(err.Error(), "packaging failed") {
				t.Fatalf("the fake signer failed instead of producing a bad artifact: %v", err)
			}
			if !strings.Contains(err.Error(), reason) {
				t.Fatalf("rejected for the wrong reason, want %q: %v", reason, err)
			}
			for _, path := range []string{output, strings.TrimSuffix(output, ".crx") + ".pem"} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("a rejected build left %s behind: %v", filepath.Base(path), err)
				}
			}
			if mode != "mutate-source" {
				if current, err := extension.Inspect(source); err != nil || current.Revision != prepared.Revision {
					t.Fatalf("source changed: %+v %v", current, err)
				}
			}
		})
	}
}

func TestInspectCRXChecksEveryProof(t *testing.T) {
	archive := zipDirectory(t, extensionFixture(t))
	inspect := func(data []byte) (extension.Description, error) {
		path := filepath.Join(t.TempDir(), "artifact.crx")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return InspectCRX(path)
	}
	for _, algorithm := range []string{"ecdsa", "rsa"} {
		key := newKey(t, algorithm)
		description, err := inspect(buildCRX(t, archive, key, crxOptions{}))
		if err != nil || description.ID != extensionIDFor(t, key) || description.Name != "Example" || !strings.HasPrefix(description.Revision, "sha256:") {
			t.Fatalf("%s: %+v %v", algorithm, description, err)
		}
	}
	key := newKey(t, "ecdsa")
	short := uint32(2)
	for name, options := range map[string]crxOptions{
		"wrong key identity":   {identityKey: newKey(t, "ecdsa")},
		"tampered payload":     {tamperPayload: true},
		"bad magic":            {magic: "Cr25"},
		"version 2":            {version: 2},
		"no proof":             {noProof: true},
		"header past the data": {headerLength: func() *uint32 { v := uint32(1 << 19); return &v }()},
		"header too short":     {headerLength: &short},
	} {
		if _, err := inspect(buildCRX(t, archive, key, options)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	valid := buildCRX(t, archive, key, crxOptions{})
	if _, err := inspect(valid[:20]); err == nil {
		t.Error("truncated file accepted")
	}
	if _, err := inspect(valid[:8]); err == nil {
		t.Error("file without a header accepted")
	}
	flipped := append([]byte(nil), valid...)
	flipped[20] ^= 0xff // inside the header: a signature or key byte
	if _, err := inspect(flipped); err == nil {
		t.Error("corrupted header accepted")
	}
}
