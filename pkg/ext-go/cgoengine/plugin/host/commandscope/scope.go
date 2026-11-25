// Package commandscope binds C-owned dependency snapshots to a local shell-call
// broker. Tokens identify consumers inside one live scope; they are not trust records.
package commandscope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

const AddressEnv = "EXT_SCOPE_ADDR"
const TokenEnv = "EXT_SCOPE_TOKEN"
const SettingsEnv = "EXT_SCOPE_SETTINGS"
const MaxBytes = 1 << 20

type Request struct {
	Args     []string          `json:"args"`
	Input    []byte            `json:"input,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
}
type Result struct {
	Exit   int    `json:"exit"`
	Output []byte `json:"output,omitempty"`
	Error  []byte `json:"error,omitempty"`
}
type Row struct {
	Manifest  packagekit.Manifest `json:"manifest"`
	Directory string              `json:"directory"`
	Digest    string              `json:"digest"`
}
type Options struct {
	Root     plugin.Identity
	Rows     []Row
	Review   func(packagekit.Manifest) (string, error)
	Dispatch func(context.Context, *Scope, plugin.Identity, Request) (Result, error)
}
type Scope struct {
	object      *library.Object
	listener    net.Listener
	packages    *store.Store
	tokens      map[plugin.Identity]string
	Plan        packagekit.Plan
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	handlers    sync.WaitGroup
	connections sync.WaitGroup
	once        sync.Once
	closeErr    error
}

// Open must run under the application's package writer. Release that writer
// before launching guests: all execution now uses the reviewed private closure.
func Open(ctx context.Context, lib *library.Described, options Options) (*Scope, error) {
	if options.Review == nil || options.Dispatch == nil {
		return nil, errors.New("scope requires review and dispatch policy")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifests := make([]packagekit.Manifest, len(options.Rows))
	for i, row := range options.Rows {
		manifests[i] = row.Manifest
	}
	var plan packagekit.Plan
	if err := lib.Library().Call("package.resolve_for", map[string]any{"packages": manifests, "os": runtime.GOOS, "arch": runtime.GOARCH}, &plan); err != nil {
		return nil, err
	}
	needed := map[plugin.Identity]bool{options.Root: true}
	for changed := true; changed; {
		changed = false
		for _, b := range plan.Bindings {
			if needed[b.Consumer] && !needed[b.Provider] {
				needed[b.Provider] = true
				changed = true
			}
		}
	}
	rows := append([]Row(nil), options.Rows...)
	for i, row := range rows {
		rows[i].Digest = strings.Repeat("0", 64)
		if needed[row.Manifest.Descriptor.Identity] {
			digest, err := options.Review(row.Manifest)
			if err != nil {
				return nil, err
			}
			rows[i].Digest = digest
		}
	}
	child, cancel := context.WithCancel(ctx)
	s := &Scope{ctx: child, cancel: cancel, tokens: map[plugin.Identity]string{}, Plan: plan}
	object, err := lib.Open("command_scope", map[string]any{"root": options.Root, "packages": rows, "os": runtime.GOOS, "arch": runtime.GOARCH, "temp_root": os.TempDir()}, library.Handlers{"dispatch": func(raw json.RawMessage) (any, error) {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, plugin.ErrDenied
		}
		s.handlers.Add(1)
		s.mu.Unlock()
		defer s.handlers.Done()
		var event struct {
			Consumer plugin.Identity
			Request  Request
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		if err := child.Err(); err != nil {
			return nil, err
		}
		result, err := options.Dispatch(child, s, event.Consumer, event.Request)
		if len(result.Output)+len(result.Error) > MaxBytes {
			return nil, errors.New("broker output exceeds limit")
		}
		return result, err
	}})
	if err != nil {
		cancel()
		return nil, err
	}
	s.object = object
	fail := func(err error) (*Scope, error) { _ = s.Close(); return nil, err }
	var view struct {
		Directory string
		Packages  []struct {
			Identity plugin.Identity
			Token    string
		}
	}
	if err := object.Call("view", nil, &view); err != nil {
		return fail(err)
	}
	for _, p := range view.Packages {
		s.tokens[p.Identity] = p.Token
	}
	s.packages, err = store.New(view.Directory, store.Limits{})
	if err != nil {
		return fail(err)
	}
	s.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail(err)
	}
	s.connections.Add(1)
	go s.serve()
	return s, nil
}
func (s *Scope) Load(id plugin.Identity) (store.Installed, error) {
	if _, ok := s.tokens[id]; !ok {
		return store.Installed{}, plugin.ErrDenied
	}
	p, err := s.packages.Load(id.ID)
	if err == nil && p.Manifest.Descriptor.Identity != id {
		err = plugin.ErrDenied
	}
	return p, err
}
func (s *Scope) Environment(id plugin.Identity) []string {
	token, ok := s.tokens[id]
	if !ok {
		return nil
	}
	return []string{AddressEnv + "=" + s.listener.Addr().String(), TokenEnv + "=" + token}
}

// Allows restricts nested invocation to one explicitly resolved direct dependency.
func (s *Scope) Allows(consumer, provider plugin.Identity, contract plugin.ContractRef, operation string) bool {
	for _, b := range s.Plan.Bindings {
		if b.Consumer == consumer && b.Provider == provider && b.Requirement.Contract == contract && b.Requirement.Operation == operation {
			return true
		}
	}
	return false
}
func (s *Scope) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.cancel()
		if s.listener != nil {
			_ = s.listener.Close()
		}
		// Join product dispatch before C can remove any snapshot it still executes.
		s.handlers.Wait()
		if s.object != nil {
			s.closeErr = s.object.Close()
		}
		s.connections.Wait()
	})
	return s.closeErr
}
func (s *Scope) serve() {
	defer s.connections.Done()
	capacity := make(chan struct{}, 16)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		select {
		case capacity <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		s.connections.Add(1)
		go func() {
			defer s.connections.Done()
			defer func() { <-capacity }()
			defer conn.Close()
			stop := context.AfterFunc(s.ctx, func() { _ = conn.Close() })
			defer stop()
			_ = conn.SetDeadline(time.Now().Add(35 * time.Second))
			var input struct {
				Token   string  `json:"token"`
				Request Request `json:"request"`
			}
			decoder := json.NewDecoder(io.LimitReader(conn, 2*MaxBytes+1))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return
			}
			var result Result
			if len(input.Request.Input) > MaxBytes {
				return
			}
			if err := s.object.Call("dispatch", input, &result); err != nil {
				result = Result{Exit: 1, Error: []byte("ext: broker denied request\n")}
			}
			_ = json.NewEncoder(conn).Encode(result)
		}()
	}
}

// Relay forwards to the host; it never loads packages or treats the address as
// authority. A forged endpoint can only supply output to its own caller.
func Relay(ctx context.Context, args []string, input io.Reader, out, stderr io.Writer) (int, error) {
	address, token := os.Getenv(AddressEnv), os.Getenv(TokenEnv)
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" || len(token) != 64 {
		return 1, errors.New("invalid command broker endpoint")
	}
	data, err := io.ReadAll(io.LimitReader(input, MaxBytes+1))
	if err != nil {
		return 1, err
	}
	if len(data) > MaxBytes {
		return 1, fmt.Errorf("broker input exceeds %d bytes", MaxBytes)
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return 1, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(35 * time.Second))
	request := Request{Args: args, Input: data}
	if raw := os.Getenv(SettingsEnv); raw != "" {
		if len(raw) > MaxBytes {
			return 1, errors.New("broker settings exceed limit")
		}
		if err := json.Unmarshal([]byte(raw), &request.Settings); err != nil {
			return 1, fmt.Errorf("invalid broker settings: %w", err)
		}
	}
	if err := json.NewEncoder(conn).Encode(map[string]any{"token": token, "request": request}); err != nil {
		return 1, err
	}
	var result Result
	if err := json.NewDecoder(io.LimitReader(conn, 2*MaxBytes+1)).Decode(&result); err != nil {
		return 1, err
	}
	if result.Exit < 0 || result.Exit > 255 || len(result.Output)+len(result.Error) > MaxBytes {
		return 1, errors.New("invalid broker response")
	}
	if _, err := out.Write(result.Output); err != nil {
		return 1, err
	}
	if _, err := stderr.Write(result.Error); err != nil {
		return 1, err
	}
	return result.Exit, nil
}

// WithEnvironment strips inherited scope hints before assigning this consumer.
func (s *Scope) WithEnvironment(values []string, id plugin.Identity) []string {
	out := make([]string, 0, len(values)+2)
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(key, AddressEnv) && !strings.EqualFold(key, TokenEnv) && !strings.EqualFold(key, SettingsEnv) {
			out = append(out, value)
		}
	}
	return append(out, s.Environment(id)...)
}
