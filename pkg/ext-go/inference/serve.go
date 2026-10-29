package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
	"github.com/webong/ext/res/inference"
)

// ServeCurrentExecutable pins the guest identity to the running executable.
func ServeCurrentExecutable(ctx context.Context, id, version string, provider inference.Provider, authorize func(context.Context, plugin.Request) error) error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	return ServeStdio(ctx, plugin.Identity{ID: id, Revision: hex.EncodeToString(h.Sum(nil)), Version: version}, provider, authorize)
}

// ServeStdio serves one caller-configured provider over the Ext JSON-line
// transport. The caller supplies the reviewed identity and admission policy.
func ServeStdio(ctx context.Context, id plugin.Identity, provider inference.Provider, authorize func(context.Context, plugin.Request) error) error {
	registry, err := author.New(id)
	if err != nil {
		return err
	}
	if _, err := RegisterProvider(registry, provider, authorize); err != nil {
		return err
	}
	guest, err := registry.Guest(author.Options{Authorize: authorize})
	if err != nil {
		return err
	}
	return jsonline.ServeStdio(ctx, guest)
}
