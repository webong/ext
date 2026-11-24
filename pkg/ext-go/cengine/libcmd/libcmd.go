// Package libcmd is the command that brings a C library of lib/ into a Go project before it builds:
// ext's builtin "ext lib". ext-go has no command of its own; a project adds ext as a Go tool and runs
// it from go generate:
//
//	go get -tool github.com/webong/ext/cmd/ext
//	//go:generate go tool ext lib fetch -lib plugin -driver cgo
//	//go:generate go tool ext lib bind -lib plugin
//
// fetch reads the library's citation (a library.Citation: where each platform's build is published
// and its SHA-256), downloads the builds for the targets, checks them, and writes a Go package the
// project blank-imports. bind writes a typed Go binding of the library from the description of its
// interface (library.Description; lib/plugin's interface.json). Where they write, and what, comes from
// the flags, then EXT_GO_DIR (the directory), then ext-go.json in the module root, then the defaults:
//
//	{
//	  "dir": "internal/extlib",                  // the parent of every library's package
//	  "libraries": {
//	    "plugin": {
//	      "citation": "third_party/ext_plugin.citation.json",  // the pin, committed to the project
//	      "description": "third_party/ext_plugin.interface.json",  // what bind binds
//	      "drivers": ["cgo"],
//	      "targets": ["linux/amd64", "darwin/arm64"]
//	    }
//	  }
//	}
//
// The default directory is internal/extlib/<library> in the module (bind writes <library>bind
// beside it), since cgo and c2go output is compiled with the project; the default target is the one
// go env reports, and the default driver is cgo when cgo is enabled and purego when it is not.
package libcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cengine/bind"
	"github.com/webong/ext/pkg/ext-go/cengine/fetch"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

type config struct {
	Dir       string                   `json:"dir"`
	Libraries map[string]libraryConfig `json:"libraries"`
}

type libraryConfig struct {
	Citation    string   `json:"citation"`
	Description string   `json:"description"`
	Drivers     []string `json:"drivers"`
	Targets     []string `json:"targets"`
	Dir         string   `json:"dir"`
	Package     string   `json:"package"`
}

const usage = `usage: ext lib fetch -lib <name> [-citation file] [-driver cgo,c2go,purego] [-target goos/goarch,...] [-dir dir] [-package name] [-offline]
       ext lib bind -lib <name> [-description file] [-dir dir] [-package name]`

// Run is "ext lib": it runs fetch or bind with args and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if err := run(context.Background(), stdout, args); err != nil {
		fmt.Fprintln(stderr, "ext lib:", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "fetch":
		return runFetch(ctx, args[1:], stdout)
	case "bind":
		return runBind(args[1:], stdout)
	case "help", "-h", "-help", "--help":
		fmt.Fprintln(stdout, usage)
		return nil
	}
	return errors.New(usage)
}

// project is the module's root and its ext-go.json.
func project() (string, config, error) {
	var cfg config
	root, err := moduleRoot()
	if err != nil {
		return "", cfg, err
	}
	if data, err := os.ReadFile(filepath.Join(root, "ext-go.json")); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return "", cfg, fmt.Errorf("ext-go.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", cfg, err
	}
	return root, cfg, nil
}

// packageDir is where a library's package goes: dir, the library's own, or <parent>/<name><suffix>,
// which must be inside the module.
func packageDir(root string, cfg config, lib, dir, own, suffix string) (string, error) {
	out := first(dir, own)
	if out == "" {
		parent := first(os.Getenv("EXT_GO_DIR"), cfg.Dir, filepath.Join("internal", "extlib"))
		out = filepath.Join(parent, strings.TrimPrefix(lib, "ext_")+suffix)
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	if rel, err := filepath.Rel(root, out); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the module at %s; the package must compile with it", out, root)
	}
	return out, nil
}

func runBind(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("bind", flag.ContinueOnError)
	lib := fs.String("lib", "", "the library, as named in ext-go.json (plugin, ledger, ...)")
	description := fs.String("description", "", "the description of the library's interface; default the one ext-go.json names")
	dir := fs.String("dir", "", "the package directory to write")
	pkg := fs.String("package", "", "the package name; default the library name without ext_")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *lib == "" {
		return errors.New("bind: -lib is required")
	}
	root, cfg, err := project()
	if err != nil {
		return err
	}
	lc := cfg.Libraries[*lib]
	file := first(*description, lc.Description)
	if file == "" {
		return fmt.Errorf("bind: no description for %s: pass -description or name one in ext-go.json", *lib)
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var d library.Description
	if err := json.Unmarshal(data, &d); err != nil {
		return fmt.Errorf("bind: %s: %w", file, err)
	}
	name := first(*pkg, strings.ReplaceAll(strings.TrimPrefix(*lib, "ext_"), "_", "")+"bind")
	out, err := packageDir(root, cfg, *lib, *dir, "", "bind")
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	rel, _ := filepath.Rel(root, file)
	src, err := bind.Generate(d, bind.Options{Package: name, Source: filepath.ToSlash(rel)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	target := filepath.Join(out, name+".go")
	if err := os.WriteFile(target, src, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ext lib: %s binding (ABI %d) in %s\n", d.Library, d.ABIVersion, target)
	return nil
}

func runFetch(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	lib := fs.String("lib", "", "the library, as named in ext-go.json (plugin, ledger, ...)")
	citation := fs.String("citation", "", "the citation file; default the one ext-go.json names")
	drivers := fs.String("driver", "", "comma-separated drivers: cgo, c2go, purego")
	targets := fs.String("target", "", `comma-separated goos/goarch targets, or "all" for every one the citation publishes`)
	dir := fs.String("dir", "", "the package directory to write")
	pkg := fs.String("package", "", "the package name; default the library name without ext_")
	offline := fs.Bool("offline", false, "use only cached archives")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *lib == "" {
		return errors.New("fetch: -lib is required")
	}
	root, cfg, err := project()
	if err != nil {
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
	out, err := packageDir(root, cfg, *lib, *dir, lc.Dir, "")
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	o.Dir = out
	if err := fetch.Fetch(ctx, o); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ext lib: %s %s (%s) for %s in %s\n", c.Library, c.Version, strings.Join(o.Drivers, ", "), strings.Join(o.Targets, ", "), out)
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
