package nativeplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

const peerCallLimit = 5 * time.Second
const peerSessionHeader = "Ext-Peer-Session"

func peerHandler(ctx context.Context, peers map[plugin.Identity]Peer, invoke bool) library.Handler {
	return func(data json.RawMessage) (any, error) {
		var event struct {
			Identity plugin.Identity `json:"identity"`
			Request  json.RawMessage `json:"request"`
		}
		if err := plugin.Decode(data, &event); err != nil {
			return nil, err
		}
		peer, ok := peers[event.Identity]
		if !ok {
			return nil, plugin.ErrNotFound
		}
		if !invoke {
			var descriptor plugin.Descriptor
			err := peer.exchange(ctx, "/descriptor", nil, &descriptor)
			return descriptor, err
		}
		var request plugin.Request
		if err := plugin.Decode(event.Request, &request); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithDeadline(ctx, request.Deadline)
		defer cancel()
		var response plugin.Response
		if err := peer.exchange(ctx, "/call", event.Request, &response); err != nil {
			return nil, err
		}
		return response, response.Validate(request.ID)
	}
}

func (p Peer) exchange(ctx context.Context, path string, body []byte, out any) error {
	if err := checkPeerSocket(p.Socket); err != nil {
		return err
	}
	dialer := net.Dialer{}
	client := http.Client{
		Timeout: peerCallLimit,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				conn, err := dialer.DialContext(ctx, "unix", p.Socket)
				if err != nil {
					return nil, err
				}
				if err := authenticatePeer(conn); err != nil {
					conn.Close()
					return nil, err
				}
				return conn, nil
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://ext-peer"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(peerSessionHeader, p.Session.ID)
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &plugin.RemoteError{Code: "timeout", Message: "peer call timed out"}
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer %s: %s", p.Descriptor.Identity.ID, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, plugin.MaxFrameBytes+1))
	if err != nil {
		return err
	}
	return plugin.Decode(data, out)
}

// ServePeer advertises a contract on an already reviewed graph and removes its
// registration on graceful shutdown. The lease bounds crash recovery.
func (g *Graph) ServePeer(ctx context.Context, descriptor plugin.Descriptor, contract plugin.ContractRef, checkReview func() error, ready func() error) (resultErr error) {
	if !peerSocketsSupported {
		return plugin.ErrUnsupported
	}
	if g.publication == nil || checkReview == nil {
		return fmt.Errorf("peer service requires a published, reviewed graph")
	}
	var sessions []struct {
		Identity plugin.Identity
		Contract plugin.ContractRef
		Session  string
	}
	if err := g.Root.Call("observe", nil, &sessions); err != nil {
		return err
	}
	var session string
	for _, node := range sessions {
		if node.Identity == descriptor.Identity && node.Contract == contract {
			session = node.Session
			break
		}
	}
	if session == "" {
		return plugin.ErrNotFound
	}
	advertised := descriptor.Clone()
	advertised.Contracts = nil
	advertised.Reverse = nil
	for _, c := range descriptor.Contracts {
		if c.ContractRef == contract {
			advertised.Contracts = []plugin.Contract{c}
			break
		}
	}

	dir, err := os.MkdirTemp("", "ext-p-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	peer := Peer{
		Session:    ledger.VertexRef{Namespace: g.publication.namespace, ID: session},
		Socket:     filepath.Join(dir, "socket"),
		Descriptor: advertised,
	}
	if !peer.valid() {
		return fmt.Errorf("invalid peer contract")
	}
	listener, err := net.Listen("unix", peer.Socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	db, err := openPeerStore(ctx, g.publication.path)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := refreshPeer(ctx, db, peer); err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, withdrawPeer(db, peer)) }()
	server := &http.Server{
		Handler: g.peerEndpoint(peer, checkReview),
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if err := authenticatePeer(conn); err != nil {
				conn.Close()
			}
			return ctx
		},
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       peerCallLimit,
		WriteTimeout:      2 * peerCallLimit,
		IdleTimeout:       time.Second,
		MaxHeaderBytes:    4096,
	}
	serving := make(chan error, 1)
	go func() { serving <- server.Serve(listener) }()
	defer server.Close()
	if err := ready(); err != nil {
		return err
	}
	ticker := time.NewTicker(peerLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serving:
			return err
		case <-ticker.C:
			if err := g.Root.Call("observe", nil, nil); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if err := refreshPeer(ctx, db, peer); err != nil && ctx.Err() == nil {
				return err
			}
		}
	}
}

func refreshPeer(ctx context.Context, db *ledger.NativeStore, peer Peer) error {
	ctx, cancel := context.WithTimeout(ctx, peerLease/3)
	defer cancel()
	for {
		err := db.PublishPeer(ctx, peer.Session.ID, peer, peerLease)
		if code := ledger.Code(err); code != "changed" && code != "busy" {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (g *Graph) peerEndpoint(peer Peer, checkReview func() error) http.Handler {
	descriptor := peer.Descriptor
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get(peerSessionHeader) != peer.Session.ID {
			http.Error(w, "unknown peer generation", http.StatusNotFound)
			return
		}
		if r.URL.Path != "/descriptor" && r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		if err := checkReview(); err != nil {
			http.Error(w, "provider approval changed", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/descriptor" {
			_ = json.NewEncoder(w).Encode(descriptor)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, plugin.MaxFrameBytes))
		var request plugin.Request
		if err != nil || plugin.Decode(data, &request) != nil || plugin.ValidateRequest(descriptor, request) != nil {
			http.Error(w, "invalid provider request", http.StatusBadRequest)
			return
		}
		timeout := min(peerCallLimit, time.Until(request.Deadline)).Milliseconds()
		if timeout <= 0 {
			http.Error(w, "provider request expired", http.StatusGatewayTimeout)
			return
		}
		var response plugin.Response
		if err := g.Root.Call("call", map[string]any{
			"call":       map[string]any{"contract": request.Contract, "operation": request.Operation, "payload": request.Payload},
			"timeout_ms": timeout,
		}, &response); err != nil {
			http.Error(w, "provider call failed", http.StatusBadGateway)
			return
		}
		response.ID = request.ID
		_ = json.NewEncoder(w).Encode(response)
	})
}
