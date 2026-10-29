// Package trust is the pure-Go reference for the C engine's scoped package
// trust records. Hosts supply a protected parent directory and serialize
// writers across processes.
package trust

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

type State uint8

const (
	Untrusted State = iota
	Trusted
	Changed
)

type Record struct {
	Scope  string `json:"scope"`
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type document struct {
	APIVersion string   `json:"apiVersion"`
	Records    []Record `json:"records"`
}

const version = "ext.trust/v1"
const maxBytes int64 = 4 << 20
const maxRecords = 4096

type File struct {
	Path string
	mu   sync.Mutex
}

func New(path string) (*File, error) {
	if path == "" {
		return nil, plugin.ErrInvalid
	}
	return &File{Path: path}, nil
}
func validKey(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '-' || c == '/' {
			continue
		}
		return false
	}
	return true
}
func validDigest(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func (f *File) load() (document, error) {
	doc := document{APIVersion: version, Records: []Record{}}
	file, err := os.Open(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return document{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return document{}, err
	}
	if int64(len(data)) > maxBytes {
		return document{}, plugin.ErrInvalid
	}
	if err := plugin.Decode(data, &doc); err != nil {
		return document{}, err
	}
	if doc.APIVersion != version || doc.Records == nil || len(doc.Records) > maxRecords {
		return document{}, plugin.ErrInvalid
	}
	seen := make(map[string]bool, len(doc.Records))
	for _, r := range doc.Records {
		if !validKey(r.Scope) || !validKey(r.ID) || !validDigest(r.Digest) {
			return document{}, plugin.ErrInvalid
		}
		k := r.Scope + "\x00" + r.ID
		if seen[k] {
			return document{}, plugin.ErrInvalid
		}
		seen[k] = true
	}
	return doc, nil
}
func index(rs []Record, scope, id string) int {
	for i, r := range rs {
		if r.Scope == scope && r.ID == id {
			return i
		}
	}
	return -1
}
func (f *File) StateOf(scope, id, current string) (State, error) {
	if !validKey(scope) || !validKey(id) || !validDigest(current) {
		return Untrusted, plugin.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.load()
	if err != nil {
		return Untrusted, err
	}
	i := index(doc.Records, scope, id)
	if i < 0 {
		return Untrusted, nil
	}
	if doc.Records[i].Digest == current {
		return Trusted, nil
	}
	return Changed, nil
}

// Records returns a copy of one scope's reviewed records for diagnostics and
// migration. A record is not evidence that the installed package still matches.
func (f *File) Records(scope string) ([]Record, error) {
	if !validKey(scope) {
		return nil, plugin.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.load()
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, record := range doc.Records {
		if record.Scope == scope {
			out = append(out, record)
		}
	}
	return out, nil
}
func (f *File) Record(scope, id, digest string) error {
	if !validKey(scope) || !validKey(id) || !validDigest(digest) {
		return plugin.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.load()
	if err != nil {
		return err
	}
	i := index(doc.Records, scope, id)
	if i < 0 {
		if len(doc.Records) >= maxRecords {
			return plugin.ErrInvalid
		}
		doc.Records = append(doc.Records, Record{scope, id, digest})
	} else {
		doc.Records[i].Digest = digest
	}
	return f.save(doc)
}
func (f *File) Revoke(scope, id string) error {
	if !validKey(scope) || !validKey(id) {
		return plugin.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.load()
	if err != nil {
		return err
	}
	i := index(doc.Records, scope, id)
	if i < 0 {
		return nil
	}
	doc.Records = append(doc.Records[:i], doc.Records[i+1:]...)
	return f.save(doc)
}

// ImportLegacy preserves existing digests exactly; a changed installed
// package stays untrusted. New-format records take precedence.
func (f *File) ImportLegacy(scope string, legacy map[string]string) error {
	if !validKey(scope) {
		return plugin.ErrInvalid
	}
	for id, digest := range legacy {
		if !validKey(id) || !validDigest(digest) {
			return plugin.ErrInvalid
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.load()
	if err != nil {
		return err
	}
	changed := false
	for id, digest := range legacy {
		if index(doc.Records, scope, id) >= 0 {
			continue
		}
		if len(doc.Records) >= maxRecords {
			return plugin.ErrInvalid
		}
		doc.Records = append(doc.Records, Record{scope, id, digest})
		changed = true
	}
	if !changed {
		return nil
	}
	return f.save(doc)
}
func (f *File) save(doc document) error {
	sort.Slice(doc.Records, func(i, j int) bool {
		a, b := doc.Records[i], doc.Records[j]
		if a.Scope == b.Scope {
			return a.ID < b.ID
		}
		return a.Scope < b.Scope
	})
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return plugin.ErrInvalid
	}
	dir := filepath.Dir(f.Path)
	tmp, err := os.CreateTemp(dir, ".ext-trust-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), f.Path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
