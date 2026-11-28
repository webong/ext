package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/home"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
	"github.com/webong/ext/src/ctx/system/capture/clipboard"
)

const clipboardGuestID = "system.clipboard.windows"
const clipboardProtocol = "ext.system.clipboard/v1"
const clipboardTrustScope = "ext.command/v1"

var clipboardContract = plugin.ContractRef{Name: "ext.system.clipboard", Version: "v1"}

// CTX selects an installed Ext resource guest. The native mechanism lives in
// that guest's platform library; this backend only carries bounded requests.
type clipboardGuestBackend struct{ session *plugin.Session }

func openClipboardBackend(ctx context.Context) (clipboard.Backend, func(), error) {
	if runtime.GOOS != "windows" {
		return nil, nil, fmt.Errorf("%w: no clipboard resource guest is available for %s", plugin.ErrUnsupported, runtime.GOOS)
	}
	s, err := store.NewWithLayout(home.Packages(), store.Limits{}, storelayout.Ext())
	if err != nil {
		return nil, nil, err
	}
	selected, err := trustedClipboardEntrypoint(s)
	if err != nil {
		return nil, nil, err
	}
	// The installed manifest and artifact were verified by ResolveEntrypoint.
	// Rechecking on every call rejects a package replaced after the handshake.
	descriptor := selected.Package.Manifest.Descriptor
	session, err := plugin.Open(ctx, descriptor, plugin.Options{
		Verify: func(_ context.Context, current plugin.Descriptor) error {
			fresh, err := store.NewWithLayout(home.Packages(), store.Limits{}, storelayout.Ext())
			if err != nil {
				return err
			}
			again, err := trustedClipboardEntrypoint(fresh)
			if err != nil || again.Digest != selected.Digest ||
				again.Artifact != selected.Artifact ||
				filepath.Clean(again.Path) != filepath.Clean(selected.Path) ||
				again.Package.Manifest.Descriptor.Identity != current.Identity {
				return plugin.ErrDenied
			}
			return nil
		},
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			return process.Start(ctx, process.Command{Path: selected.Path, Args: []string{"run"}, Env: os.Environ()})
		},
		Authorize: func(ctx context.Context, _ plugin.Request) error { return ctx.Err() },
		Timeout:   10 * time.Second,
	})
	if err != nil {
		return nil, nil, err
	}
	return clipboardGuestBackend{session: session}, func() { _ = session.Abort() }, nil
}

func trustedClipboardEntrypoint(s *store.Store) (store.Entrypoint, error) {
	installed, err := s.Load(clipboardGuestID)
	if err != nil {
		return store.Entrypoint{}, fmt.Errorf("clipboard resource guest is not installed: %w", err)
	}
	digest, err := installed.Digest()
	if err != nil {
		return store.Entrypoint{}, err
	}
	file, err := trust.New(filepath.Join(home.Packages(), ".package-trust.json"))
	if err != nil {
		return store.Entrypoint{}, err
	}
	state, err := file.StateOf(clipboardTrustScope, clipboardGuestID, digest)
	if err != nil {
		return store.Entrypoint{}, err
	}
	if state != trust.Trusted {
		return store.Entrypoint{}, fmt.Errorf("%w: clipboard resource guest requires ext package trust %s", plugin.ErrDenied, clipboardGuestID)
	}
	return s.ResolveEntrypoint(clipboardGuestID, clipboardContract.Name, clipboardContract.Version, clipboardProtocol, digest)
}

func (b clipboardGuestBackend) call(ctx context.Context, operation string, input any, output any) error {
	request, err := json.Marshal(input)
	if err != nil {
		return err
	}
	response, err := b.session.Call(ctx, clipboardContract, operation, request)
	if err != nil {
		return err
	}
	return json.Unmarshal(response, output)
}

func (b clipboardGuestBackend) Generation(ctx context.Context) (string, error) {
	var result struct {
		Generation string `json:"generation"`
	}
	if err := b.call(ctx, "generation", struct{}{}, &result); err != nil {
		return "", err
	}
	return result.Generation, nil
}

func (b clipboardGuestBackend) Read(ctx context.Context, maxBytes int64, formats []string) (clipboard.Snapshot, error) {
	var result clipboard.Snapshot
	err := b.call(ctx, "read", struct {
		MaxBytes int64    `json:"max_bytes"`
		Formats  []string `json:"formats"`
	}{maxBytes, formats}, &result)
	return result, err
}

func (b clipboardGuestBackend) Write(ctx context.Context, request clipboard.WriteRequest, maxBytes int64, formats []string) (clipboard.Receipt, error) {
	var result clipboard.Receipt
	operation := "write"
	if len(request.Items) == 0 {
		operation = "clear"
	}
	err := b.call(ctx, operation, struct {
		ExpectedGeneration string           `json:"expected_generation"`
		MaxBytes           int64            `json:"max_bytes"`
		Formats            []string         `json:"formats"`
		Items              []clipboard.Item `json:"items"`
	}{request.ExpectedGeneration, maxBytes, formats, request.Items}, &result)
	return result, err
}

var _ clipboard.Backend = clipboardGuestBackend{}
