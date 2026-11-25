package sourcecmd

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// importCatalog snapshots a verified directory catalog into ordinary pinned
// archive sources. It neither mutates the catalog nor activates its packages.
func (m Manager) importCatalog(root string, out io.Writer) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	lib, err := nativeplugin.Open()
	if err != nil {
		return err
	}
	catalog, err := lib.Open("store", map[string]any{"root": root, "layout": storelayout.Ext()}, nil)
	if err != nil {
		return err
	}
	defer catalog.Close()
	var rows []struct {
		Directory string
		Manifest  packagekit.Manifest
	}
	if err := catalog.Call("list", nil, &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("catalog contains no packages")
	}
	if err := os.MkdirAll(m.Home, 0700); err != nil {
		return err
	}
	lock := filepath.Join(m.Home, ".sources.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("source writer lock: %w", err)
	}
	defer os.Remove(lock)
	doc, err := m.load()
	if err != nil {
		return err
	}
	if len(doc.Sources)+len(rows) > 256 {
		return fmt.Errorf("source limit reached")
	}
	archiveRoot := filepath.Join(m.Home, "source-archives")
	if err := os.MkdirAll(archiveRoot, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(archiveRoot, "catalog-")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		id := row.Manifest.Descriptor.Identity.ID
		nameHash := sha256.Sum256([]byte(id))
		name := "catalog-" + hex.EncodeToString(nameHash[:8])
		if _, exists := doc.Sources[name]; exists {
			return fmt.Errorf("source %s already exists for %s; remove it explicitly before importing again", name, id)
		}
		dir := filepath.Join(stage, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			return err
		}
		archive := filepath.Join(dir, "package.zip")
		pin, err := archiveDirectory(row.Directory, archive)
		if err != nil {
			return err
		}
		// Re-extract and verify the exact bytes being registered, so a changing
		// catalog cannot turn an earlier successful list into an unverified source.
		verify, err := os.MkdirTemp("", "ext-source-verify-")
		if err != nil {
			return err
		}
		staged, verifyErr := lib.Open("store", map[string]any{"root": filepath.Join(verify, "packages"), "layout": storelayout.Ext()}, nil)
		if verifyErr == nil {
			verifyErr = staged.Call("install_archive", map[string]any{"archive": archive, "sha256": pin, "id": id, "preserve_executable": true}, nil)
			_ = staged.Close()
		}
		_ = os.RemoveAll(verify)
		if verifyErr != nil {
			return verifyErr
		}
		index := map[string]any{"format_version": 1, "name": id, "packages": map[string]any{runtime.GOOS + "/" + runtime.GOARCH: map[string]string{"url": "package.zip", "sha256": pin}}}
		data, err := json.Marshal(index)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, "index.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
		source, err := m.snapshot(path)
		if err != nil {
			return err
		}
		doc.Sources[name] = source
		names = append(names, name)
	}
	if err := m.save(doc); err != nil {
		return err
	}
	published = true
	for _, name := range names {
		fmt.Fprintf(out, "%s\t%s\tinstall: ext package install --source %s\n", name, identity(doc.Sources[name]), name)
	}
	return nil
}

// Archive a bounded snapshot, preserving executable bits but never links.
func archiveDirectory(root, path string) (string, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	writer := zip.NewWriter(io.MultiWriter(file, hash))
	var total int64
	count := 0
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("catalog contains a nonregular file")
		}
		count++
		if count > 10000 || info.Size() < 0 || info.Size() > store.DefaultMaxArchiveBytes-total {
			return fmt.Errorf("catalog package exceeds archive limits")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !store.SafeArchivePath(relative) || strings.HasPrefix(relative, "../") {
			return fmt.Errorf("unsafe catalog path")
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relative
		header.Method = zip.Store
		dest, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(dest, io.LimitReader(input, info.Size()+1))
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != info.Size() {
			return fmt.Errorf("catalog file changed while importing")
		}
		total += n
		return nil
	})
	zipErr := writer.Close()
	closeErr := file.Close()
	if walkErr != nil {
		return "", walkErr
	}
	if zipErr != nil {
		return "", zipErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
