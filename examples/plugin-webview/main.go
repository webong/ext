// Command plugin-webview runs a WASI plugin guest inside a browser or webview
// and talks ext.plugin/v1 to it. It is a proof of concept for hosting
// WebAssembly plugins in the system web engine, driven by the web engine in
// res/web/bundle. See README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/webong/ext/res/web/bundle"
)

func sh(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %v: %s", name, args, err, out)
	}
	return nil
}

func main() {
	root := flag.String("root", "site", "directory holding index.html, worker.js and guest.wasm")
	target := flag.String("target", "Safari", `a macOS application name, "ios-simulator", or "android"`)
	timeout := flag.Duration("timeout", 90*time.Second, "overall time limit")
	flag.Parse()
	result, err := bundle.Run(context.Background(), bundle.Options{
		Root: *root, Timeout: *timeout, StartupTimeout: 60 * time.Second, HeartbeatTimeout: 20 * time.Second,
		// A blocking stdin queue needs SharedArrayBuffer, which needs isolation.
		CrossOriginIsolation: true,
		Console:              func(e bundle.Event) { fmt.Printf("page %-9s %s\n", e.Level, e.Text) },
		Open: func(ctx context.Context, raw string) error {
			switch *target {
			case "ios-simulator":
				return sh(ctx, "xcrun", "simctl", "openurl", "booted", raw)
			case "android":
				u, err := url.Parse(raw)
				if err != nil {
					return err
				}
				if err := sh(ctx, "adb", "reverse", "tcp:"+u.Port(), "tcp:"+u.Port()); err != nil {
					return err
				}
				return sh(ctx, "adb", "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", raw)
			}
			return sh(ctx, "open", "-a", *target, raw)
		},
	})
	fmt.Printf("result: status=%s exit=%d reason=%q\n", result.Status, result.ExitCode, result.Reason)
	if err != nil || result.Status != bundle.StatusExited {
		os.Exit(1)
	}
	os.Exit(result.ExitCode)
}
