// Command ext-go brings a C library of lib/ into a Go project before it builds.
//
// Add it as a tool of the project and run it from go generate:
//
//	go get -tool github.com/webong/ext/pkg/ext-go/cengine/cmd/ext-go
//	//go:generate go tool ext-go fetch -lib plugin -driver cgo
//
// fetch reads the library's citation (a library.Citation: where each platform's build is published
// and its SHA-256), downloads the builds for the targets, checks them, and writes a Go package the
// project blank-imports. Where it writes, and what, comes from the flags, then EXT_GO_DIR (the
// directory), then ext-go.json in the module root, then the defaults:
//
//	{
//	  "dir": "internal/extlib",                  // the parent of every library's package
//	  "libraries": {
//	    "plugin": {
//	      "citation": "third_party/ext_plugin.citation.json",  // the pin, committed to the project
//	      "drivers": ["cgo"],
//	      "targets": ["linux/amd64", "darwin/arm64"]
//	    }
//	  }
//	}
//
// The default directory is internal/extlib/<library> in the module, since cgo and c2go output is
// compiled with the project; the default target is the one go env reports, and the default driver is
// cgo when cgo is enabled and purego when it is not.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cengine/fetch"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

type config struct {
	Dir       string                   `json:"dir"`
	Libraries map[string]libraryConfig `json:"libraries"`
}

type libraryConfig struct {
	Citation string   `json:"citation"`
	Drivers  []string `json:"drivers"`
	Targets  []string `json:"targets"`
	Dir      string   `json:"dir"`
	Package  string   `json:"package"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ext-go:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "fetch" {
		return errors.New("usage: ext-go fetch -lib <name> [-citation file] [-driver cgo,c2go,purego] [-target goos/goarch,...] [-dir dir] [-package name] [-offline]")
	}
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	lib := fs.String("lib", "", "the library, as named in ext-go.json (plugin, ledger, ...)")
	citation := fs.String("citation", "", "the citation file; default the one ext-go.json names")
	drivers := fs.String("driver", "", "comma-separated drivers: cgo, c2go, purego")
	targets := fs.String("target", "", `comma-separated goos/goarch targets, or "all" for every one the citation publishes`)
	dir := fs.String("dir", "", "the package directory to write")
	pkg := fs.String("package", "", "the package name; default the library name without ext_")
	offline := fs.Bool("offline", false, "use only cached archives")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *lib == "" {
		return errors.New("fetch: -lib is required")
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	var cfg config
	if data, err := os.ReadFile(filepath.Join(root, "ext-go.json")); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("ext-go.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	lc := cfg.Libraries[*lib]
	citationFile := first(*citation, lc.Citation)
	if citationFile == "" {
		return fmt.Errorf("fetch: no citation for %s: pass -citation or name one in ext-go.json", *lib)
	}
	if !filepath.IsAbs(citationFile) {
		citationFile = filepath.Join(root, citationFile)
	}
	c, err := library.ReadCitation(citationFile)
	if err != nil {
		return err
	}
	o := fetch.Options{Citation: c, Package: first(*pkg, lc.Package), Offline: *offline}
	o.Drivers = list(*drivers, lc.Drivers)
	if len(o.Drivers) == 0 {
		if goEnv("CGO_ENABLED") == "1" {
			o.Drivers = []string{fetch.CGo}
		} else {
			o.Drivers = []string{fetch.PureGo}
		}
	}
	o.Targets = list(*targets, lc.Targets)
	switch {
	case len(o.Targets) == 1 && o.Targets[0] == "all":
		o.Targets = nil
		for _, a := range c.Assets {
			o.Targets = append(o.Targets, a.Targets...)
		}
	case len(o.Targets) == 0:
		o.Targets = []string{goEnv("GOOS") + "/" + goEnv("GOARCH")}
	}
	out := first(*dir, lc.Dir)
	if out == "" {
		parent := first(os.Getenv("EXT_GO_DIR"), cfg.Dir, filepath.Join("internal", "extlib"))
		out = filepath.Join(parent, strings.TrimPrefix(*lib, "ext_"))
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	if rel, err := filepath.Rel(root, out); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("fetch: %s is outside the module at %s; the package must compile with it", out, root)
	}
	o.Dir = out
	if err := fetch.Fetch(ctx, o); err != nil {
		return err
	}
	fmt.Printf("ext-go: %s %s (%s) for %s in %s\n", c.Library, c.Version, strings.Join(o.Drivers, ", "), strings.Join(o.Targets, ", "), out)
	return nil
}

// moduleRoot is the directory of the go.mod that governs the working directory.
func moduleRoot() (string, error) {
	gomod := goEnv("GOMOD")
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("fetch: run inside a Go module")
	}
	return filepath.Dir(gomod), nil
}

func goEnv(name string) string {
	if v := os.Getenv(name); v != "" && name != "GOMOD" {
		return v
	}
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func list(flagValue string, fallback []string) []string {
	if flagValue == "" {
		return fallback
	}
	var out []string
	for _, v := range strings.Split(flagValue, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
