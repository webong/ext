package pkgcmd

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
)

// A command package can use the plugin protocol over a private local connection.
// Its standard streams then retain their ordinary CLI meaning, including binary
// input. This is an ext command transport, not a new plugin engine backend.
func runSocket(installed store.Installed, declared Declared, args []string, stdout, stderr io.Writer) int {
	name := declared.Name
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := installed.Verify(); err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	path, err := installed.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	defer listener.Close()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	token := hex.EncodeToString(secret[:])
	dir, _ := os.Getwd()
	cmd := exec.Command(path)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	cmd.Env = commandEnvironment(listener.Addr().String(), token, installed)
	files, err := attachFDs(cmd, args, declared.FDFlags)
	if err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	if err = cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := acceptGuest(connectCtx, listener, token)
	if err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	defer conn.Close()
	var client *jsonline.Client
	session, err := plugin.Open(connectCtx, installed.Manifest.Descriptor, plugin.Options{
		Verify: func(context.Context, plugin.Descriptor) error { return installed.Verify() },
		Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
			client = jsonline.NewClient(conn)
			return client, nil
		},
		Authorize: func(context.Context, plugin.Request) error { return nil },
	})
	if err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	defer session.Abort()
	payload, err := json.Marshal(request{Command: name, Args: args, Dir: dir})
	if err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	ref := plugin.ContractRef{Name: Contract, Version: Version}
	raw, err := session.Call(ctx, ref, Operation, payload)
	if err != nil {
		// A downstream command such as `grep -q` may close stdout as soon as
		// it finds a match. The product guest then receives SIGPIPE, just as a
		// direct CLI would; avoid turning that expected pipeline exit into a
		// misleading plugin protocol diagnostic.
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			finished := make(chan error, 1)
			go func() { finished <- cmd.Wait() }()
			select {
			case exit := <-finished:
				waited = true
				if brokenPipeExit(exit) {
					return 141
				}
			case <-time.After(200 * time.Millisecond):
				_ = cmd.Process.Kill()
				<-finished
				waited = true
			}
		}
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	var result response
	if err := json.Unmarshal(raw, &result); err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	return result.Exit
}

func commandEnvironment(address, token string, installed store.Installed) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "EXT_COMMAND_") {
			env = append(env, item)
		}
	}
	hostExecutable, _ := os.Executable()
	env = append(env,
		"EXT_COMMAND_ADDR="+address,
		"EXT_COMMAND_TOKEN="+token,
		"EXT_COMMAND_ID="+installed.Name(),
		"EXT_COMMAND_REVISION="+installed.Manifest.Descriptor.Identity.Revision,
		"EXT_COMMAND_VERSION="+installed.Version(),
	)
	if installed.Name() == "ext/ctx" {
		env = append(env, "EXT_HOST_EXECUTABLE="+hostExecutable, "EXT_PRODUCT_PREFIX=ctx")
	}
	return env
}

func acceptGuest(ctx context.Context, listener net.Listener, token string) (net.Conn, error) {
	go func() { <-ctx.Done(); _ = listener.Close() }()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var supplied [65]byte
		_, err = io.ReadFull(conn, supplied[:])
		_ = conn.SetReadDeadline(time.Time{})
		if err == nil && supplied[64] == '\n' && subtle.ConstantTimeCompare(supplied[:64], []byte(token)) == 1 {
			return conn, nil
		}
		_ = conn.Close()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
	}
}
