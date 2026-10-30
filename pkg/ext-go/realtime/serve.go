package realtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
	domain "github.com/webong/ext/res/int/realtime"
)

func ServeStdio(ctx context.Context, identity plugin.Identity, provider domain.Provider, authorize func(context.Context, plugin.Request) error) error {
	registry, err := author.New(identity)
	if err != nil {
		return err
	}
	_, state, err := registerProvider(registry, provider, authorize)
	if err != nil {
		return err
	}
	guest, err := registry.Guest(author.Options{Authorize: authorize})
	if err != nil {
		return err
	}
	defer provider.Close()
	defer state.closeSession()
	return jsonline.ServeStdio(ctx, guest)
}

func ServeCurrentExecutable(ctx context.Context, id, version string, provider domain.Provider, authorize func(context.Context, plugin.Request) error) error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	return ServeStdio(ctx, plugin.Identity{ID: id, Version: version, Revision: hex.EncodeToString(hash.Sum(nil))}, provider, authorize)
}
