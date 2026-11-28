// Package nativelib finds the C libraries ext runs on, lib/plugin and lib/ledger, and brings them in
// when they are missing. ext is self-bootstrapping: a binary with nothing beside it downloads the exact
// release it was built against, as pinned by the citations it embeds, and keeps it in the user cache.
//
// A library is resolved, in order, from:
//  1. its environment variable (EXT_PLUGIN_LIBRARY, EXT_LEDGER_LIBRARY), which is final;
//  2. a copy embedded in this binary (ext bundle fetch -driver purego);
//  3. for the ledger, the directory of EXT_PLUGIN_LIBRARY;
//  4. ext-lib beside the executable, as the release bundle lays it out;
//  5. the release its citation pins: a verified copy in the user cache, else a download, unless
//     EXT_OFFLINE is set.
package nativelib

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/fetch"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

// A Lib is one C library ext runs on.
type Lib struct {
	Name string // its C prefix and citation name, such as ext_plugin
	Env  string // the variable that names its path
	base string // its file name without the platform's prefix and suffix
}

var (
	Plugin = Lib{Name: "ext_plugin", Env: "EXT_PLUGIN_LIBRARY", base: "ext_plugin_host"}
	Ledger = Lib{Name: "ext_ledger", Env: "EXT_LEDGER_LIBRARY", base: "ext_ledger"}
)

// File is the library's file name on goos, as ext-lib holds it.
func (l Lib) File(goos string) string {
	switch goos {
	case "darwin":
		return "lib" + l.base + ".dylib"
	case "windows":
		return l.base + ".dll"
	}
	return "lib" + l.base + ".so"
}

// ErrUnavailable is a library none of the places it may come from provides.
var ErrUnavailable = errors.New("unavailable")

//go:embed citations
var embedded embed.FS

// The settings Resolve reads; tests replace them.
var (
	citations             = loadCitations
	cacheDir              = ""
	download              = fetch.HTTPS
	notice      io.Writer = os.Stderr
	executable            = os.Executable
	fetchBudget           = 10 * time.Minute
)

type result struct {
	path string
	err  error
}

var (
	mu       sync.Mutex
	resolved = map[string]result{}
)

// Resolve returns the path of l's shared library, bringing it in if needed. The result is kept for the
// life of the process.
func Resolve(l Lib) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if r, ok := resolved[l.Name]; ok {
		return r.path, r.err
	}
	path, err := resolve(l)
	if err != nil {
		err = fmt.Errorf("%s %w: %v", l.Name, ErrUnavailable, err)
	}
	resolved[l.Name] = result{path, err}
	return path, err
}

// Open resolves l and loads it.
func Open(l Lib) (*library.Library, error) {
	path, err := Resolve(l)
	if err != nil {
		return nil, err
	}
	return library.Open(path)
}

// Environment is what a child process is given so it finds the libraries this one resolved without
// looking again: the variable of each library that resolved.
func Environment() []string {
	var env []string
	for _, l := range []Lib{Plugin, Ledger} {
		if path, err := Resolve(l); err == nil {
			env = append(env, l.Env+"="+path)
		}
	}
	return env
}

func resolve(l Lib) (string, error) {
	if path := os.Getenv(l.Env); path != "" {
		return path, nil
	}
	if c, ok := library.Provided(l.Name); ok {
		return library.Materialize(c, l.Name, cacheDir)
	}
	file := l.File(runtime.GOOS)
	var looked []string
	if l == Ledger {
		if plugin := os.Getenv(Plugin.Env); plugin != "" {
			beside := filepath.Join(filepath.Dir(plugin), file)
			if exists(beside) {
				return beside, nil
			}
			looked = append(looked, beside)
		}
	}
	if exe, err := executable(); err == nil {
		bundled := filepath.Join(filepath.Dir(exe), "ext-lib", file)
		if exists(bundled) {
			return bundled, nil
		}
		looked = append(looked, bundled)
	}
	cited, err := citations()
	if err != nil {
		return "", err
	}
	citation, ok := cited[l.Name]
	if !ok {
		return "", fmt.Errorf("set %s to its shared library or install it in %s; this ext build pins no release to download",
			l.Env, strings.Join(looked, " or "))
	}
	offline := os.Getenv("EXT_OFFLINE") != ""
	ctx, cancel := context.WithTimeout(context.Background(), fetchBudget)
	defer cancel()
	target := runtime.GOOS + "/" + runtime.GOARCH
	path, err := fetch.Shared(ctx, fetch.SharedOptions{
		Citation: citation,
		Target:   target,
		Dir:      dirOf("library"),
		Cache:    dirOf("archives"),
		Offline:  offline,
		Fetch: func(ctx context.Context, url string) (io.ReadCloser, error) {
			fmt.Fprintf(notice, "ext: downloading %s %s for %s from %s\n", l.Name, citation.Version, target, url)
			return download(ctx, url)
		},
	})
	if errors.Is(err, fetch.ErrOffline) {
		return "", fmt.Errorf("EXT_OFFLINE is set and %s %s is not in the cache; unset it, set %s, or install it in %s",
			l.Name, citation.Version, l.Env, strings.Join(looked, " or "))
	}
	return path, err
}

// dirOf is a directory of fetch's under the cache ext keeps, or empty for fetch's own default.
func dirOf(name string) string {
	if cacheDir == "" {
		return ""
	}
	return filepath.Join(cacheDir, name)
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// loadCitations reads the citations this binary embeds, one <library>.citation.json per library.
func loadCitations() (map[string]library.Citation, error) {
	out := map[string]library.Citation{}
	entries, err := fs.ReadDir(embedded, "citations")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".citation.json")
		if !ok {
			continue
		}
		data, err := embedded.ReadFile("citations/" + e.Name())
		if err != nil {
			return nil, err
		}
		c, err := library.ParseCitation(data)
		if err != nil {
			return nil, fmt.Errorf("embedded %s: %w", e.Name(), err)
		}
		if c.Library != name {
			return nil, fmt.Errorf("embedded %s cites %s", e.Name(), c.Library)
		}
		out[name] = c
	}
	return out, nil
}
