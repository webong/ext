package nativeplugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/webong/ext/cmd/ext/internal/nativelib"
	"github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// Every invocation owns a new namespace. A later process never clears another
// owner's observations, even after a crash. These are observations, not leases.
type publication struct {
	store     *ledger.NativeStore
	namespace string
	path      string
	revision  uint64
	owner     ledger.Vertex
}

func ledgerLibrary() (*ledger.Library, error) {
	native, err := nativelib.Open(nativelib.Ledger)
	if err != nil {
		return nil, err
	}
	return ledger.Bind(native)
}

func publishGraph(ctx context.Context, graph *library.Object, path string, peers map[plugin.Identity]Peer) (_ *publication, resultErr error) {
	lib, err := ledgerLibrary()
	if err != nil {
		return nil, err
	}
	db, err := lib.OpenStore(ctx, "file", path, ledger.Options{})
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = db.Close()
		}
	}()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	ns := "ext.execution." + hex.EncodeToString(token[:])
	var sessions []struct {
		Session  string
		PID      *int
		Identity plugin.Identity
	}
	if err := graph.Call("observe", nil, &sessions); err != nil {
		return nil, err
	}
	refs := make([]map[string]any, 0, len(sessions))
	peerRefs := map[string]ledger.VertexRef{}
	for _, session := range sessions {
		if session.PID == nil {
			refs = append(refs, nil)
			peerRefs[session.Session] = peers[session.Identity].Session
			continue
		}
		pid := *session.PID
		var observed struct {
			HostID    string `json:"host_id"`
			Processes []struct {
				PID     int
				StartID string `json:"start_id"`
			}
		}
		if err := lib.Collect(ctx, "process", map[string]any{"pid": pid, "resource_limit": 1}, &observed); err != nil {
			return nil, err
		}
		if len(observed.Processes) != 1 || observed.Processes[0].PID != pid {
			return nil, fmt.Errorf("execution process disappeared before publication")
		}
		var ref map[string]any
		if err := lib.Objects().Library().Call("process_identity", map[string]any{"host_id": observed.HostID, "pid": pid, "start_id": observed.Processes[0].StartID}, &ref); err != nil {
			return nil, err
		}
		ref["session"] = session.Session
		refs = append(refs, ref)
	}
	var projection struct {
		Schema      ledger.Schema
		Transaction ledger.Transaction
	}
	if err := graph.Call("snapshot", map[string]any{"namespace": ns, "processes": refs}, &projection); err != nil {
		return nil, err
	}
	for _, vertex := range projection.Transaction.Vertices {
		if ref, ok := peerRefs[vertex.ID]; ok {
			vertex.Attributes["reference"] = ref
		}
	}
	projection.Schema.Kinds = append(projection.Schema.Kinds, ns+"/owner")
	owner := ledger.Vertex{ID: "owner", Kind: ns + "/owner", Attributes: map[string]any{"pid": os.Getpid(), "state": "observed", "observed_at": time.Now().UTC().Format(time.RFC3339Nano)}}
	projection.Transaction.Vertices = append(projection.Transaction.Vertices, owner)
	if err := db.Register(ctx, projection.Schema); err != nil {
		return nil, err
	}
	commit, err := db.Reconcile(ctx, ledger.Snapshot{Namespace: ns, Vertices: projection.Transaction.Vertices, Edges: projection.Transaction.Edges})
	if err != nil {
		return nil, err
	}
	return &publication{store: db, namespace: ns, path: path, revision: commit.Revision, owner: owner}, nil
}

func (p *publication) finish() error {
	p.owner.Attributes["state"] = "closed"
	p.owner.Attributes["closed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	_, err := p.store.Reconcile(context.Background(), ledger.Snapshot{Namespace: p.namespace, Revision: p.revision, Vertices: []ledger.Vertex{p.owner}, Edges: []ledger.Edge{}})
	return err
}
