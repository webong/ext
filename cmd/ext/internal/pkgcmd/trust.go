package pkgcmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
)

// TrustFile names the legacy command trust file. Existing digests are imported
// unchanged once; a new trust file is then authoritative.
const TrustFile = "command-trust.json"

// Keep the existing scope: package trust in this host does not grant trust
// in adapter or other consumer scopes. The name predates non-command packages.
const scope = "ext.command/v1"
const sharedTrustFile = ".package-trust.json"

// Trust records this host's approval of a reviewed installed package digest.
type Trust struct {
	file     *hosttrust.File
	Packages map[string]string
}

func LoadTrust(home string) (*Trust, error) {
	path := filepath.Join(home, "packages", sharedTrustFile)
	file, err := hosttrust.New(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		legacyPath := filepath.Join(home, TrustFile)
		data, readErr := os.ReadFile(legacyPath)
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return nil, readErr
		}
		if readErr == nil {
			var old struct {
				Packages map[string]string `json:"packages"`
			}
			if err := plugin.Decode(data, &old); err != nil {
				return nil, err
			}
			if len(old.Packages) > 0 {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return nil, err
				}
				if err := file.ImportLegacy(scope, old.Packages); err != nil {
					return nil, err
				}
			}
		}
	} else if err != nil {
		return nil, err
	}
	records, err := file.Records(scope)
	if err != nil {
		return nil, err
	}
	adapterRecords, err := file.Records("ctx.adapter/v2")
	if err != nil {
		return nil, err
	}
	records = append(records, adapterRecords...)
	t := &Trust{file: file, Packages: map[string]string{}}
	for _, r := range records {
		t.Packages[r.ID] = r.Digest
	}
	return t, nil
}

func (t *Trust) Trusted(installed store.Installed) (bool, error) {
	return t.trustedIn(installed, packageScope(installed))
}

func (t *Trust) trustedIn(installed store.Installed, trustScope string) (bool, error) {
	digest, err := installed.Digest()
	if err != nil {
		return false, err
	}
	state, err := t.file.StateOf(trustScope, installed.Name(), digest)
	return state == hosttrust.Trusted, err
}
func (t *Trust) Trust(installed store.Installed) error {
	digest, err := installed.Digest()
	if err != nil {
		return err
	}
	// Explicit package approval records the host scope; adapters additionally
	// need their existing consumer scope. Migration never performs this widening.
	if err := t.file.Record(scope, installed.Name(), digest); err != nil {
		return err
	}
	if err := t.file.Record(packageScope(installed), installed.Name(), digest); err != nil {
		return err
	}
	t.Packages[installed.Name()] = digest
	return nil
}
func (t *Trust) Revoke(id string) error {
	if err := t.file.Revoke(scope, id); err != nil {
		return err
	}
	if err := t.file.Revoke("ctx.adapter/v2", id); err != nil {
		return err
	}
	delete(t.Packages, id)
	return nil
}

// Retain separate scopes so migrating an adapter never authorizes commands.
func packageScope(p store.Installed) string {
	for _, c := range p.Manifest.Descriptor.Contracts {
		if c.Name == plugin.AdapterContractName {
			return "ctx.adapter/v2"
		}
	}
	return scope
}
