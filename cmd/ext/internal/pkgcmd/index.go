package pkgcmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

type indexArchive struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

func writeIndex(output string, archives []string) (string, error) {
	if len(archives) == 0 || len(archives) > 256 {
		return "", fmt.Errorf("expected 1 to 256 package archives")
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(output)
	lib, err := nativeplugin.Open()
	if err != nil {
		return "", err
	}
	work, err := os.MkdirTemp("", "ext-package-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	index := struct {
		FormatVersion int                     `json:"format_version"`
		Name          string                  `json:"name"`
		Packages      map[string]indexArchive `json:"packages"`
	}{FormatVersion: 1, Packages: make(map[string]indexArchive, len(archives))}
	for i, path := range archives {
		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}
		name := filepath.Base(path)
		if filepath.Dir(path) != dir || strings.EqualFold(path, output) || !store.SafeArchivePath(name) || strings.ContainsAny(name, " %?#:") {
			return "", fmt.Errorf("archive %q needs a URL-safe filename beside the index", path)
		}
		digest, err := indexDigest(path)
		if err != nil {
			return "", err
		}
		// Each target gets a fresh store. Identical package revisions can have
		// different manifest bytes across targets.
		root := filepath.Join(work, fmt.Sprint(i))
		staged, err := lib.Open("store", map[string]any{"root": root}, nil)
		if err != nil {
			return "", err
		}
		var manifest packagekit.Manifest
		err = staged.Call("install_archive", map[string]any{"archive": path, "sha256": digest, "preserve_executable": true}, &manifest)
		staged.Close()
		if err != nil {
			return "", fmt.Errorf("verify %s: %w", name, err)
		}
		if err := os.RemoveAll(root); err != nil {
			return "", err
		}
		if index.Name == "" {
			index.Name = manifest.Name()
		} else if index.Name != manifest.Name() {
			return "", fmt.Errorf("index cannot mix different packages")
		}
		target, err := indexTarget(manifest)
		if err != nil {
			return "", fmt.Errorf("archive %s: %w", name, err)
		}
		if _, exists := index.Packages[target]; exists {
			return "", fmt.Errorf("index repeats platform %s", target)
		}
		index.Packages[target] = indexArchive{URL: name, SHA256: digest}
	}
	if err := lib.Library().Call("catalog.validate", index, nil); err != nil {
		return "", err
	}
	contents, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return "", err
	}
	contents = append(contents, '\n')
	pending, err := os.CreateTemp(dir, ".package-index-")
	if err != nil {
		return "", err
	}
	defer os.Remove(pending.Name())
	_, writeErr := pending.Write(contents)
	if err := pending.Close(); err != nil {
		return "", err
	}
	if writeErr != nil {
		return "", writeErr
	}
	if err := os.Chmod(pending.Name(), 0644); err != nil {
		return "", err
	}
	if err := os.Rename(pending.Name(), output); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func indexDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > store.DefaultMaxArchiveBytes {
		return "", fmt.Errorf("%s is not a bounded regular archive", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, store.DefaultMaxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if n > store.DefaultMaxArchiveBytes {
		return "", fmt.Errorf("archive %s exceeds size limit", path)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func indexTarget(manifest packagekit.Manifest) (string, error) {
	var os, arch string
	for _, asset := range manifest.Artifacts {
		if asset.OS != "" {
			if os != "" && os != asset.OS {
				return "", fmt.Errorf("package declares more than one operating system")
			}
			os = asset.OS
		}
		if asset.Arch != "" {
			if arch != "" && arch != asset.Arch {
				return "", fmt.Errorf("package declares more than one architecture")
			}
			arch = asset.Arch
		}
	}
	if os == "" || arch == "" {
		return "", fmt.Errorf("package must declare an operating system and architecture")
	}
	return os + "/" + arch, nil
}
