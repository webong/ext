package pkgcmd

import (
	"path/filepath"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
)

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
	return readTrust(file)
}

func readTrust(file *hosttrust.File) (*Trust, error) {
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
	// The user approved this package's reviewed bytes. Record that approval for
	// each capability the manifest declares; consumers still choose policy and
	// authorize individual calls.
	if err := t.file.Record(scope, installed.Name(), digest); err != nil {
		return err
	}
	if err := t.file.Record(packageScope(installed), installed.Name(), digest); err != nil {
		return err
	}
	for _, contract := range installed.Manifest.Descriptor.Contracts {
		if err := t.file.Record(contract.Name+"/"+contract.Version, installed.Name(), digest); err != nil {
			return err
		}
	}
	t.Packages[installed.Name()] = digest
	return nil
}
func (t *Trust) Revoke(id string) error {
	if err := t.file.RevokeAll(id); err != nil {
		return err
	}
	delete(t.Packages, id)
	return nil
}

// Package status uses the relevant declared capability scope. The approval
// ledger itself keeps each scope separate.
func packageScope(p store.Installed) string {
	for _, c := range p.Manifest.Descriptor.Contracts {
		if c.Name == plugin.AdapterContractName {
			return "ctx.adapter/v2"
		}
	}
	if len(p.Manifest.Descriptor.Contracts) == 1 {
		c := p.Manifest.Descriptor.Contracts[0]
		return c.Name + "/" + c.Version
	}
	return scope
}
