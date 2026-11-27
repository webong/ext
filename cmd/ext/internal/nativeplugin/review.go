package nativeplugin

import (
	"fmt"
	"path/filepath"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
)

// ReviewInstalled returns the approved digest of a locally installed package.
func ReviewInstalled(lib *library.Described, root string, layout storelayout.Layout, id string) (string, error) {
	var digest, state string
	if err := lib.Library().Call("store_digest", map[string]any{"root": root, "id": id, "layout": layout}, &digest); err != nil {
		return "", err
	}
	if err := lib.Library().Call("trust_state", map[string]any{
		"path": filepath.Join(root, ".package-trust.json"), "scope": "ext.command/v1", "id": id, "digest": digest,
	}, &state); err != nil {
		return "", err
	}
	if state != "trusted" {
		return "", fmt.Errorf("required package %s is not approved", id)
	}
	return digest, nil
}
