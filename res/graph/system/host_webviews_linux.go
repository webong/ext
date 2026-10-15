//go:build linux

package systemgraph

import (
	"bufio"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func platformWebviews(ctx context.Context) ([]WebviewInfo, error) {
	directories, err := linuxWebviewDirectories(ctx)
	if err != nil {
		return nil, err
	}
	types := []struct{ prefix, name, engine, api, abi string }{
		{"libwebkit2gtk-4.0.so", "WebKitGTK", "webkit", "WebKitWebView", "4.0"},
		{"libwebkit2gtk-4.1.so", "WebKitGTK", "webkit", "WebKitWebView", "4.1"},
		{"libwebkitgtk-6.0.so", "WebKitGTK", "webkit", "WebKitWebView", "6.0"},
		{"libQt5WebEngineCore.so", "Qt WebEngine", "blink", "QtWebEngineCore", "5"},
		{"libQt6WebEngineCore.so", "Qt WebEngine", "blink", "QtWebEngineCore", "6"},
	}
	result := []WebviewInfo{}
	seen := map[string]bool{}
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmtHostReadError("shared library directory", err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.IsDir() {
				continue
			}
			for _, kind := range types {
				if entry.Name() != kind.prefix && !strings.HasPrefix(entry.Name(), kind.prefix+".") {
					continue
				}
				path := filepath.Join(directory, entry.Name())
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil || seen[resolved] {
					continue // Ignore dangling links and aliases of the same library.
				}
				library, err := elf.Open(resolved)
				if err != nil {
					continue // A linker script or unrelated file is not a runtime.
				}
				shared := library.Type == elf.ET_DYN
				architecture := library.Machine.String()
				if err := library.Close(); err != nil {
					return nil, fmtHostReadError("shared library", err)
				}
				if !shared {
					continue
				}
				seen[resolved] = true
				result = append(result, WebviewInfo{Name: kind.name, Engine: kind.engine, API: kind.api, ABIVersion: kind.abi,
					Location: path, ResolvedPath: resolved, Architecture: architecture, Scope: "shared", Source: "shared-library"})
			}
		}
	}
	return result, ctx.Err()
}

func linuxWebviewDirectories(ctx context.Context) ([]string, error) {
	result := []string{}
	seen := map[string]bool{}
	add := func(path string) {
		if filepath.IsAbs(path) {
			path = filepath.Clean(path)
			if !seen[path] {
				result = append(result, path)
				seen[path] = true
			}
		}
	}
	for _, path := range filepath.SplitList(os.Getenv("LD_LIBRARY_PATH")) {
		add(path)
	}
	for _, path := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/usr/local/lib", "/usr/local/lib64"} {
		add(path)
	}
	for _, pattern := range []string{"/lib/*-linux-gnu", "/usr/lib/*-linux-gnu"} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			add(path)
		}
	}
	// Follow the loader's declared directories without running libraries or
	// recursively walking application installations. Bound include recursion.
	visited := map[string]bool{}
	var readConfig func(string, int) error
	readConfig = func(path string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		path = filepath.Clean(path)
		if visited[path] {
			return nil
		}
		if depth > 16 || len(visited) >= 256 {
			return fmt.Errorf("too many shared library configuration includes")
		}
		visited[path] = true
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmtHostReadError("shared library configuration", err)
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line, _, _ := strings.Cut(scanner.Text(), "#")
			line = strings.TrimSpace(line)
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if fields[0] != "include" {
				add(line)
				continue
			}
			for _, pattern := range fields[1:] {
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(filepath.Dir(path), pattern)
				}
				includes, err := filepath.Glob(pattern)
				if err != nil {
					return fmtHostReadError("shared library configuration include", err)
				}
				for _, include := range includes {
					if err := readConfig(include, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return scanner.Err()
	}
	if err := readConfig("/etc/ld.so.conf", 0); err != nil {
		return nil, err
	}
	return result, nil
}
