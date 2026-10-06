package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testDescriptor() Descriptor {
	return Descriptor{APIVersion: APIVersion, Identity: Identity{ID: "example/worker", Revision: "r1", Version: "1.0.0"}, Contracts: []Contract{{ContractRef: ContractRef{Name: "example.work", Version: "v1"}, Operations: []Operation{{Name: "inspect", Surface: "observation"}, {Name: "apply", Surface: "action"}}}}}
}

func TestSelectionAndHandshake(t *testing.T) {
	d := testDescriptor()
	want := Requirement{Contract: d.Contracts[0].ContractRef, Operation: "inspect"}
	other := d.Clone()
	other.Identity.ID = "example/other"
	if _, err := Select([]Descriptor{d, other}, want); !errors.Is(err, ErrAmbiguous) {
		t.Fatal(err)
	}
	want.Identity = &d.Identity
	selected, err := Select([]Descriptor{other, d}, want)
	if err != nil {
		t.Fatal(err)
	}
	selected.Contracts[0].Operations[0].Name = "changed"
	if d.Contracts[0].Operations[0].Name != "inspect" {
		t.Fatal("selection aliases caller memory")
	}
	want.Contract.Version = "v2"
	if _, err := Select([]Descriptor{d}, want); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	reordered := d.Clone()
	reordered.Contracts[0].Operations[0], reordered.Contracts[0].Operations[1] = reordered.Contracts[0].Operations[1], reordered.Contracts[0].Operations[0]
	if err := MatchHandshake(d, reordered); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Descriptor){
		func(v *Descriptor) { v.Identity.Revision = "r2" },
		func(v *Descriptor) { v.Identity.Version = "1.0.1" },
		func(v *Descriptor) { v.Contracts[0].Operations[0].Surface = "action" },
		func(v *Descriptor) {
			v.Contracts[0].Operations = append(v.Contracts[0].Operations, Operation{Name: "remove"})
		},
		func(v *Descriptor) { v.Contracts[0].Operations = v.Contracts[0].Operations[:1] },
	} {
		changed := d.Clone()
		change(&changed)
		if err := MatchHandshake(d, changed); !errors.Is(err, ErrMismatch) {
			t.Fatalf("accepted changed handshake: %v", err)
		}
	}
}

func TestStrictEnvelopes(t *testing.T) {
	d := testDescriptor()
	for _, change := range []func(*Descriptor){
		func(v *Descriptor) { v.APIVersion = "ctx.plugin/v2" },
		func(v *Descriptor) { v.Identity.Revision = "" },
		func(v *Descriptor) { v.Contracts = append(v.Contracts, v.Contracts[0]) },
		func(v *Descriptor) {
			v.Contracts[0].Operations = append(v.Contracts[0].Operations, v.Contracts[0].Operations[0])
		},
		func(v *Descriptor) { v.Contracts[0].Operations[0].Name = "plugin.hello" },
	} {
		bad := d.Clone()
		change(&bad)
		if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{"apiVersion":"ctx.plugin/v1","unknown":true}`,
		`{"apiVersion":"ctx.plugin/v1","apiVersion":"wrong"}`,
		`{"payload":{"x":1,"x":2}}`,
		`{} {}`,
	} {
		var r Response
		if err := Decode([]byte(raw), &r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	r := Request{APIVersion: APIVersion, ID: "1", Plugin: d.Identity, Contract: d.Contracts[0].ContractRef, Operation: "inspect", Surface: "observation", Deadline: time.Now().Add(time.Minute), Payload: json.RawMessage(`{"value":1e1000}`)}
	if err := ValidateRequest(d, r); err != nil {
		t.Fatal(err)
	}
	r.Surface = "action"
	if err := ValidateRequest(d, r); !errors.Is(err, ErrMismatch) {
		t.Fatal(err)
	}
	response := Response{APIVersion: APIVersion, ID: "1", Payload: json.RawMessage("null"), Error: &RemoteError{Code: "denied"}}
	if err := response.Validate("1"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestDirectoryDigestCompatibilityAndTampering(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	files := []struct{ path, content string }{{"assets/a", "asset"}, {"plugin", "binary"}}
	outer := sha256.New()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.path), []byte(f.content), 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(f.content))
		fmt.Fprintf(outer, "./%s %s\n", f.path, hex.EncodeToString(hash[:]))
	}
	digest, err := DirectoryDigest(dir)
	if err != nil || digest != hex.EncodeToString(outer.Sum(nil)) {
		t.Fatalf("digest %s: %v", digest, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets/a"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := DirectoryDigest(dir)
	if err != nil || changed == digest {
		t.Fatalf("tampering missed: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "plugin"), filepath.Join(dir, "link")); err != nil {
		t.Skip("symlink not supported")
	}
	if _, err := DirectoryDigest(dir); !errors.Is(err, ErrInvalid) {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func TestRejectInvalidUTF8(t *testing.T) {
	var raw json.RawMessage
	if Decode([]byte{'"', 0xff, '"'}, &raw) == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestDecodeRejectsUnpairedSurrogates(t *testing.T) {
	for _, tc := range []struct {
		text string
		ok   bool
	}{
		{`"🙂"`, true}, {`"🙂"`, true}, {`"é"`, true}, {`"\\ud83d"`, true}, {`"\\\\ud83d"`, true}, {`"\"🙂"`, true},
		{`"\ud83d"`, false}, {`"\ude42"`, false}, {`"\ud83dx"`, false}, {`"\ud83dA"`, false}, {`"\ude42\ud83d"`, false}, {`"\ud83d🙂"`, false},
		{`{"k\ud83d":1}`, false}, {`["ok","\udc00"]`, false},
	} {
		var v any
		err := Decode([]byte(tc.text), &v)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.text, err, tc.ok)
		}
	}
}
