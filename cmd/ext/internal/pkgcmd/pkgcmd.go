// Package pkgcmd runs commands that installed ext packages declare. A package
// declares its commands in the metadata key "ext.command/v1" and serves the
// contract ext.command/v1, whose operation "run" executes one of them; ext runs the
// package as a guest and shows the output it streams.
package pkgcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"sync"

	"github.com/webong/ext/cmd/ext/internal/registry"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/host"
	"github.com/webong/ext/pkg/plugin-go/engine/host/store"
)

const (
	// MetadataKey is the manifest metadata key that lists a package's commands.
	MetadataKey = "ext.command/v1"
	// Contract and Version name the contract whose operation Operation runs a command.
	Contract  = "ext.command"
	Version   = "v1"
	Operation = "run"
)

// RunContract is the contract a package serves to provide commands. Its run
// operation declares progress, because the command's output arrives as log events.
var RunContract = plugin.Contract{
	ContractRef: plugin.ContractRef{Name: Contract, Version: Version},
	Operations:  []plugin.Operation{{Name: Operation, Progress: true}},
}

// OutputLimits is the event rate a command may stream at. It is generous because
// output that is dropped is output the user never sees; the guest coalesces within it.
var OutputLimits = plugin.ProgressLimits{EventsPerSecond: 10000, BytesPerSecond: 16 << 20}

// Declared is one command a package declares.
type Declared struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Declare reads the commands a package declares. A package that does not declare
// the metadata key, or does not serve the ext.command contract, declares none.
func Declare(installed store.Installed) ([]Declared, error) {
	raw, ok := installed.Manifest.Metadata[MetadataKey]
	if !ok || !installed.Supports(Contract, Version) {
		return nil, nil
	}
	var doc struct {
		Commands []Declared `json:"commands"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", installed.Name(), MetadataKey, err)
	}
	seen := map[string]bool{}
	for _, c := range doc.Commands {
		if !namePattern.MatchString(c.Name) {
			return nil, fmt.Errorf("%s: %s: invalid command name %q", installed.Name(), MetadataKey, c.Name)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("%s: %s: command %q is declared twice", installed.Name(), MetadataKey, c.Name)
		}
		seen[c.Name] = true
	}
	return doc.Commands, nil
}

// request and response are the payloads of the run operation.
type request struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Dir     string   `json:"dir,omitempty"`
}

type response struct {
	Exit int `json:"exit"`
}

// Command returns the registry entry that runs the declared command of installed.
// options are passed to host.Open for every launch.
func Command(installed store.Installed, declared Declared, options host.Options) registry.Command {
	return registry.Command{
		Name:    declared.Name,
		Source:  registry.Package,
		Version: installed.Version(),
		Run: func(args []string, stdout, stderr io.Writer) int {
			return run(installed, options, declared.Name, args, stdout, stderr)
		},
	}
}

func run(installed store.Installed, options host.Options, name string, args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	dir, _ := os.Getwd()
	proc, err := host.Open(ctx, installed, options)
	if err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	defer proc.Close()

	out := newWriter(stdout, stderr)
	var resp response
	err = proc.CallProgress(ctx, Contract+"."+Operation, request{Command: name, Args: args, Dir: dir}, &resp, out.event, plugin.CallOptions{Progress: OutputLimits})
	out.finish()
	if err != nil {
		fmt.Fprintf(stderr, "ext: %s: %v\n", name, err)
		return 1
	}
	return resp.Exit
}

// writer moves the command's output to the terminal off the connection's reader,
// which must not block, in the order the events arrived.
type writer struct {
	stdout, stderr io.Writer
	mu             sync.Mutex
	cond           *sync.Cond
	queue          []plugin.Progress
	closed         bool
	done           chan struct{}
}

func newWriter(stdout, stderr io.Writer) *writer {
	w := &writer{stdout: stdout, stderr: stderr, done: make(chan struct{})}
	w.cond = sync.NewCond(&w.mu)
	go w.loop()
	return w
}

func (w *writer) event(p plugin.Progress) {
	w.mu.Lock()
	w.queue = append(w.queue, p)
	w.mu.Unlock()
	w.cond.Signal()
}

func (w *writer) finish() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.cond.Signal()
	<-w.done
}

func (w *writer) loop() {
	defer close(w.done)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.closed {
			w.cond.Wait()
		}
		batch := w.queue
		w.queue = nil
		closed := w.closed
		w.mu.Unlock()
		for _, p := range batch {
			switch {
			case p.Kind == "log" && p.Stream == "err":
				io.WriteString(w.stderr, p.Text)
			case p.Kind == "log":
				io.WriteString(w.stdout, p.Text)
			}
			if p.Dropped > 0 {
				fmt.Fprintf(w.stderr, "ext: %d output events were dropped\n", p.Dropped)
			}
		}
		if closed && len(batch) == 0 {
			return
		}
	}
}
