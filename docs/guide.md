# Using ext

ext is a set of Go libraries and two binaries. This page is organised by what you want
to do. Every Go recipe here was run from a scratch module outside this repository, with
no workspace and no `replace`, against the published tags, and its output is shown. If a
recipe does not work for you, that is a bug in this page.

The libraries need Go 1.23 or newer, except `pkg/plugin-hashicorp`, which needs Go 1.26
(its gRPC dependencies require it). Each library is its own module, so you depend on only
what you use:

| You want to | Module (version used here) |
| --- | --- |
| host or write a plugin | `github.com/webong/ext/pkg/plugin` (v0.2.0) |
| run WebAssembly in a sandbox | `github.com/webong/ext/pkg/plugin-wasm` (v0.2.0) |
| discover the machine | `github.com/webong/ext/pkg/graph` (v0.1.0) |
| run web content in a browser | `github.com/webong/ext/res/web` (v0.1.1) |

```sh
go get github.com/webong/ext/pkg/plugin@v0.2.0
```

## Host a plugin

A plugin declares typed methods; a host opens a session and calls them. The host decides
what is allowed (`allow` below is a policy you replace), and the plugin never sees more
than the host gives it. This runs both sides in one process; the same session API works
over other backends (JSON lines on a pipe, HashiCorp go-plugin, a WebAssembly guest).

```go
// Host a plugin: declare one typed method, register it, open a session, call it.
package main

import (
	"context"
	"fmt"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/author"
	"github.com/webong/ext/pkg/plugin/inprocess"
)

type Greeting struct {
	Name string `json:"name"`
}

func main() {
	ctx := context.Background()
	greet := author.Method[Greeting, Greeting]{
		Contract:  plugin.ContractRef{Name: "example.greet", Version: "v1"},
		Operation: plugin.Operation{Name: "greet", Surface: "observation"},
	}
	registry, err := author.New(plugin.Identity{ID: "example/greeter", Revision: "1"})
	check(err)
	check(author.Register(registry, greet, func(_ context.Context, _ plugin.Request, in Greeting) (Greeting, error) {
		return Greeting{Name: "hello, " + in.Name}, nil
	}))
	allow := func(context.Context, plugin.Request) error { return nil } // a real host applies its own policy
	guest, err := registry.Guest(author.Options{Authorize: allow})
	check(err)
	backend, err := inprocess.New(guest)
	check(err)
	descriptor := registry.Descriptor()
	session, err := plugin.Open(ctx, descriptor, plugin.Options{
		Verify:    func(_ context.Context, actual plugin.Descriptor) error { return plugin.MatchHandshake(descriptor, actual) },
		Authorize: allow,
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return backend, nil },
	})
	check(err)
	defer session.Close(ctx)
	out, err := author.Call(ctx, session, greet, Greeting{Name: "ext"})
	check(err)
	fmt.Println(out.Name)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
```

Output: `hello, ext`. See [the plugin SDK](plugin-sdk.md) for configuration, instance
leases, health, streams, and the other backends.

## Run WebAssembly in a sandbox

`RunCommand` runs a WASI command with nothing from the host: no environment, no files,
no network, unless you grant them in `CommandOptions`. A context deadline stops even a
module that never does I/O. `Inspect` reads a module's imports without running it, so
you can tell a command from a web module or a component first.

```go
// Run a WebAssembly command with nothing from the host, and stop it on a deadline.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	wasm "github.com/webong/ext/pkg/plugin-wasm"
)

// (module (func (export "_start")))
var quick = []byte{0, 97, 115, 109, 1, 0, 0, 0, 1, 4, 1, 96, 0, 0, 3, 2, 1, 0, 7, 10, 1, 6, 95, 115, 116, 97, 114, 116, 0, 0, 10, 4, 1, 2, 0, 11}

// (module (func (export "_start") (loop (br 0)))): never finishes.
var forever = []byte{0, 97, 115, 109, 1, 0, 0, 0, 1, 4, 1, 96, 0, 0, 3, 2, 1, 0, 7, 10, 1, 6, 95, 115, 116, 97, 114, 116, 0, 0, 10, 9, 1, 7, 0, 3, 64, 12, 0, 11, 11}

func main() {
	code, err := wasm.RunCommand(context.Background(), quick, wasm.CommandOptions{})
	fmt.Println("quick module: exit", code, "err", err)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = wasm.RunCommand(ctx, forever, wasm.CommandOptions{})
	fmt.Println("endless module stopped by its deadline:", errors.Is(err, context.DeadlineExceeded))

	info, err := wasm.Inspect(context.Background(), quick)
	fmt.Println("inspect:", info.Target, "imports:", len(info.Imports), "err", err)
}
```

Output:

```
quick module: exit 0 err <nil>
endless module stopped by its deadline: true
inspect: none imports: 0 err <nil>
```

## Discover the machine

Collectors report shells, filesystems and webviews without starting anything. Note the
package is named `systemgraph`, so import it with that name.

```go
// Discover what the machine has, without starting anything.
package main

import (
	"context"
	"fmt"

	systemgraph "github.com/webong/ext/pkg/graph/system"
)

func main() {
	host, err := systemgraph.DiscoverHost(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Printf("shells: %d, filesystems: %d\n", len(host.Shells), len(host.Filesystems))
	webviews, err := systemgraph.DiscoverWebviews(context.Background())
	fmt.Printf("webviews found: %d (err: %v)\n", len(webviews), err)
}
```

Output on one machine: `shells: 7, filesystems: 14` and `webviews found: 1`. See
[host discovery](host-discovery.md) for the graph store and process supervision.

## Run web content in a browser you choose

The web engine serves a folder from loopback under a policy that blocks the network by
default, gives the page `window.ext.log` and `window.ext.exit`, and returns what the page
did. Showing the page is your decision, which is the `Open` function: a desktop browser,
an iOS Simulator, an Android emulator or a device. Run this from the directory that holds
`site/`.

```go
// Run a web page in a browser you choose, and read what it reports.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/webong/ext/res/web/bundle"
)

func main() {
	result, err := bundle.Run(context.Background(), bundle.Options{
		Root:    "site",
		Timeout: time.Minute,
		Console: func(e bundle.Event) { fmt.Printf("page %s: %s\n", e.Level, e.Text) },
		Open: func(ctx context.Context, url string) error { // the host decides how to show the page
			if runtime.GOOS == "darwin" {
				return exec.CommandContext(ctx, "open", url).Run()
			}
			return exec.CommandContext(ctx, "xdg-open", url).Run()
		},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("status:", result.Status, "exit code:", result.ExitCode)
}
```

with this `site/index.html`:

```html
<!doctype html><title>demo</title><script>
console.log("hello from the page");
fetch("https://example.com/", {mode: "no-cors"}).then(() => console.log("network: reached"), () => console.log("network: blocked"))
  .then(() => window.ext.exit(0));
</script>
```

Output:

```
page log: hello from the page
page log: network: blocked
status: exited exit code: 0
```

The page's outside request is blocked by default; set `Policy.AllowNet` to open it. The
engine's limits, including that it is not a complete sandbox, are in
[the web engine doc](web-engine.md).

> Use `res/web` v0.1.1 or newer. In v0.1.0 console lines could arrive out of order, and an
> exit could overtake the lines logged just before it.

## Check content from the command line

`ctn verify` runs a file through an engine and reports `PASS`, `FAIL`, `TIMEOUT` or
`UNSUPPORTED` with the file's SHA-256, and only when the engine reports that it confines
its content. It needs an engine adapter installed and trusted through `ctx`.

```sh
ctn verify module.wasm
ctn verify --timeout 5s --json contract.hex
```

See [ctn verify](ctn-verify.md) for the rules, verdicts and exit statuses.

## Where to go next

- [Engine adapters](engine-adapters.md): the contract for the `jvm`, `wasm` and `evm` engines.
- [Adapter API](adapter-api.md): write an adapter for a tool.
- [Mobile WebAssembly](adr-mobile-wasm.md): hosting plugins on a phone, and what is and is not verified.
