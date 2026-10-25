package pkgcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/webong/ext/pkg/ext-go/engine/host/store"
)

// TrustFile is the name of the trust record inside the ext home.
const TrustFile = "command-trust.json"

// Trust records which installed packages the user has allowed to run as commands.
// A record binds a package id to the digest of its installed content, so a package
// that changes after it was trusted is not trusted until the user trusts it again.
// Installing a package never trusts it; publisher trust stays with ext, not with the
// package's own metadata.
type Trust struct {
	path     string
	Packages map[string]string `json:"packages"`
}

// LoadTrust reads home/command-trust.json; a missing file means nothing is trusted.
func LoadTrust(home string) (*Trust, error) {
	t := &Trust{path: filepath.Join(home, TrustFile), Packages: map[string]string{}}
	data, err := os.ReadFile(t.path)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, t); err != nil {
		return nil, fmt.Errorf("%s: %w", t.path, err)
	}
	if t.Packages == nil {
		t.Packages = map[string]string{}
	}
	return t, nil
}

// Trusted reports whether installed matches the digest recorded for its id.
func (t *Trust) Trusted(installed store.Installed) (bool, error) {
	want, ok := t.Packages[installed.Name()]
	if !ok {
		return false, nil
	}
	got, err := installed.Digest()
	if err != nil {
		return false, err
	}
	return got == want, nil
}

// Trust records the current content of installed as trusted.
func (t *Trust) Trust(installed store.Installed) error {
	digest, err := installed.Digest()
	if err != nil {
		return err
	}
	t.Packages[installed.Name()] = digest
	return t.save()
}

// Revoke forgets the record for id.
func (t *Trust) Revoke(id string) error {
	delete(t.Packages, id)
	return t.save()
}

func (t *Trust) save() error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(t.path), TrustFile+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), t.path)
}
