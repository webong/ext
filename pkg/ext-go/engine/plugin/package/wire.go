package packagekit

import (
	"bytes"
	"encoding/json"
	"fmt"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// The Go model retains Descriptor for API users; ext.io/v1 has one top-level
// identity and capability declaration. Runtime descriptors remain ext.io.pc/v1.
type manifestBody Manifest
type manifestWire struct {
	*manifestBody
	Kind      string            `json:"kind"`
	Identity  plugin.Identity   `json:"identity"`
	Contracts []plugin.Contract `json:"contracts"`
	Reverse   []plugin.Contract `json:"reverse,omitempty"`
}

func (m Manifest) MarshalJSON() ([]byte, error) {
	return json.Marshal(manifestWire{(*manifestBody)(&m), "package", m.Descriptor.Identity, m.Descriptor.Contracts, m.Descriptor.Reverse})
}
func (m *Manifest) UnmarshalJSON(data []byte) error {
	var body manifestBody
	w := manifestWire{manifestBody: &body}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&w); err != nil {
		return err
	}
	if w.APIVersion != Version || w.Kind != "package" {
		return fmt.Errorf("unsupported manifest: expected ext.io/v1 package")
	}
	*m = Manifest(body)
	m.Descriptor = plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: w.Identity, Contracts: w.Contracts, Reverse: w.Reverse}
	return nil
}
