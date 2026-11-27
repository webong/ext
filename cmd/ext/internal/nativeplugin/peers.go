package nativeplugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

const peerLease = 3 * time.Second

// LedgerPath selects the ledger used by Ext and its product hosts.
func LedgerPath(home string) string {
	if path := os.Getenv("EXT_LEDGER_PATH"); path != "" {
		return path
	}
	return filepath.Join(home, "ledger")
}

// Peer identifies a live provider session owned by another local Ext host.
type Peer struct {
	Session    ledger.VertexRef  `json:"session"`
	Socket     string            `json:"socket"`
	Descriptor plugin.Descriptor `json:"descriptor"`
	digest     string
}

func (p Peer) Manifest() packagekit.Manifest {
	return packagekit.Manifest{APIVersion: packagekit.Version, Descriptor: p.Descriptor}
}

func (p Peer) valid() bool {
	return len(p.Descriptor.Contracts) == 1 && len(p.Descriptor.Reverse) == 0 && p.Descriptor.Validate() == nil &&
		p.Session.ID != "" && p.Session.Namespace != "" && filepath.IsAbs(p.Socket)
}

func openPeerStore(ctx context.Context, path string) (*ledger.NativeStore, error) {
	lib, err := ledgerLibrary()
	if err != nil {
		return nil, err
	}
	return lib.OpenStore(ctx, "file", path, ledger.Options{})
}

// LivePeers returns current advertisements. A handshake independently checks
// that the selected registration can still be reached.
func LivePeers(ctx context.Context, path string) ([]Peer, error) {
	if !peerSocketsSupported {
		return nil, nil
	}
	db, err := openPeerStore(ctx, path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Peers(ctx)
	if err != nil {
		if errors.Is(err, library.ErrNotDescribed) && os.Getenv("EXT_LEDGER_PATH") == "" {
			// Existing bundled ledgers can still host local resource calls.
			return nil, nil
		}
		return nil, err
	}
	var peers []Peer
	for _, row := range rows {
		var peer Peer
		digest, _ := row.Attributes["binding_digest"].(string)
		if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
			continue
		}
		data, err := json.Marshal(row.Attributes["registration"])
		if err == nil && plugin.Decode(data, &peer) == nil && peer.valid() && row.ID == peer.Session.ID {
			peer.digest = digest
			peers = append(peers, peer)
		}
	}
	return peers, nil
}

func withdrawPeer(db *ledger.NativeStore, peer Peer) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return db.WithdrawPeer(ctx, peer.Session.ID)
}
