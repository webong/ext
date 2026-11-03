// Package extension provides portable WebExtension inspection, packaging,
// staging, artifact helpers, and adapter-neutral types. Adapters own native
// signing, installation, and activation.
package extension

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxFiles        = 2048
	maxFileSize     = 16 << 20
	maxArchiveSize  = 64 << 20
	maxManifestSize = 1 << 20
)

// Description is source-free metadata suitable for review before packaging.
type Description struct {
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	ManifestVersion int      `json:"manifestVersion"`
	ID              string   `json:"id,omitempty"`
	Permissions     []string `json:"permissions"`
	HostPermissions []string `json:"hostPermissions"`
	Files           int      `json:"files"`
	Bytes           int64    `json:"bytes"`
	Revision        string   `json:"revision"`
}

type sourceFile struct {
	name string
	mode fs.FileMode
	open func() (io.ReadCloser, error)
	size int64
}

// Inspect accepts an unpacked extension directory, ZIP, or XPI with manifest.json
// at its root (a single wrapping directory is also accepted).
func Inspect(source string) (Description, error) {
	files, closeSource, err := readSource(source)
	if err != nil {
		return Description{}, err
	}
	defer closeSource()
	return inspectFiles(files)
}

// Package creates a deterministic ZIP after inspecting the complete source.
// A ZIP is a build artifact; it is not a signed CRX or a browser installation.
func Package(source, output string) (Description, error) {
	files, closeSource, err := readSource(source)
	if err != nil {
		return Description{}, err
	}
	defer closeSource()
	description, err := inspectFiles(files)
	if err != nil {
		return Description{}, err
	}
	if output == "" {
		return Description{}, errors.New("output path is required")
	}
	inputAbs, _ := filepath.Abs(source)
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return Description{}, err
	}
	if outputAbs == inputAbs || strings.HasPrefix(outputAbs, inputAbs+string(filepath.Separator)) {
		return Description{}, errors.New("output must be outside the source")
	}
	if _, err := os.Stat(outputAbs); err == nil {
		return Description{}, errors.New("output already exists")
	} else if !os.IsNotExist(err) {
		return Description{}, err
	}
	parent := filepath.Dir(outputAbs)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return Description{}, err
	}
	temporary, err := os.CreateTemp(parent, ".extension-*.zip")
	if err != nil {
		return Description{}, err
	}
	defer os.Remove(temporary.Name())
	archive := zip.NewWriter(temporary)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetModTime(time.Unix(0, 0).UTC())
		header.SetMode(0644)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			archive.Close()
			temporary.Close()
			return Description{}, err
		}
		reader, err := file.open()
		if err != nil {
			archive.Close()
			temporary.Close()
			return Description{}, err
		}
		n, copyErr := io.Copy(entry, io.LimitReader(reader, maxFileSize+1))
		err = copyErr
		closeErr := reader.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil && n != file.size {
			err = fmt.Errorf("extension file %q changed while packaging", file.name)
		}
		if err != nil {
			archive.Close()
			temporary.Close()
			return Description{}, err
		}
	}
	if err := archive.Close(); err != nil {
		temporary.Close()
		return Description{}, err
	}
	if err := temporary.Close(); err != nil {
		return Description{}, err
	}
	packaged, err := Inspect(temporary.Name())
	if err != nil {
		return Description{}, err
	}
	if packaged.Revision != description.Revision {
		return Description{}, errors.New("extension source changed while packaging")
	}
	// Link creates the final name exclusively, so a concurrent writer cannot
	// replace an existing package between validation and publication.
	if err := os.Link(temporary.Name(), outputAbs); err != nil {
		return Description{}, err
	}
	return description, nil
}

// Stage extracts validated source into a new directory chosen by the caller.
// It never edits a browser profile or claims the extension is installed.
func Stage(source, destination string) (Description, error) {
	return StageWithRevision(source, destination, "")
}

// StageWithRevision rejects a source that differs from an inspected revision.
// It also verifies the files after copying, before reporting the staged path.
func StageWithRevision(source, destination, expectedRevision string) (Description, error) {
	files, closeSource, err := readSource(source)
	if err != nil {
		return Description{}, err
	}
	defer closeSource()
	description, err := inspectFiles(files)
	if err != nil {
		return Description{}, err
	}
	if expectedRevision != "" && expectedRevision != description.Revision {
		return Description{}, errors.New("extension revision changed since inspection")
	}
	if destination == "" {
		return Description{}, errors.New("destination is required")
	}
	inputAbs, _ := filepath.Abs(source)
	destAbs, err := filepath.Abs(destination)
	if err != nil {
		return Description{}, err
	}
	if destAbs == inputAbs || strings.HasPrefix(destAbs, inputAbs+string(filepath.Separator)) {
		return Description{}, errors.New("destination must be outside the source")
	}
	if _, err := os.Stat(destAbs); err == nil {
		return Description{}, errors.New("destination already exists")
	} else if !os.IsNotExist(err) {
		return Description{}, err
	}
	parent := filepath.Dir(destAbs)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return Description{}, err
	}
	if err := os.Mkdir(destAbs, 0755); err != nil {
		return Description{}, err
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(destAbs)
		}
	}()
	for _, file := range files {
		target := filepath.Join(destAbs, filepath.FromSlash(file.name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return Description{}, err
		}
		reader, err := file.open()
		if err != nil {
			return Description{}, err
		}
		writer, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			reader.Close()
			return Description{}, err
		}
		n, copyErr := io.Copy(writer, io.LimitReader(reader, maxFileSize+1))
		closeWriteErr := writer.Close()
		closeReadErr := reader.Close()
		if copyErr != nil {
			return Description{}, copyErr
		}
		if n != file.size {
			return Description{}, fmt.Errorf("extension file %q changed while staging", file.name)
		}
		if closeWriteErr != nil {
			return Description{}, closeWriteErr
		}
		if closeReadErr != nil {
			return Description{}, closeReadErr
		}
	}
	staged, err := Inspect(destAbs)
	if err != nil {
		return Description{}, err
	}
	if staged.Revision != description.Revision {
		return Description{}, errors.New("extension source changed while staging")
	}
	if expectedRevision != "" && staged.Revision != expectedRevision {
		return Description{}, errors.New("staged extension differs from approved revision")
	}
	if staged.Name != description.Name || staged.Version != description.Version {
		return Description{}, errors.New("staged extension manifest changed")
	}
	completed = true
	return description, nil
}

func readSource(source string) ([]sourceFile, func(), error) {
	if source == "" {
		return nil, nil, errors.New("extension source is required")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("extension source cannot be a symlink")
	}
	if info.IsDir() {
		files, err := directoryFiles(source)
		return files, func() {}, err
	}
	if !info.Mode().IsRegular() || (!strings.EqualFold(filepath.Ext(source), ".zip") && !strings.EqualFold(filepath.Ext(source), ".xpi")) {
		return nil, nil, errors.New("extension source must be a directory, ZIP, or XPI")
	}
	reader, err := zip.OpenReader(source)
	if err != nil {
		return nil, nil, err
	}
	files, err := zipFiles(reader.File)
	if err != nil {
		reader.Close()
		return nil, nil, err
	}
	return files, func() { _ = reader.Close() }, nil
}

func directoryFiles(root string) ([]sourceFile, error) {
	var files []sourceFile
	err := filepath.WalkDir(root, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("extension contains symlink %q", full)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("extension contains non-file %q", full)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if err := safeName(name); err != nil {
			return err
		}
		filePath := full
		files = append(files, sourceFile{name: name, size: info.Size(), mode: info.Mode(), open: func() (io.ReadCloser, error) { return os.Open(filePath) }})
		if len(files) > maxFiles {
			return errors.New("extension has too many files")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return normalizeFiles(files)
}

func zipFiles(entries []*zip.File) ([]sourceFile, error) {
	if len(entries) > maxFiles*2 {
		return nil, errors.New("extension ZIP has too many entries")
	}
	var files []sourceFile
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name, "/")
		if err := safeName(name); err != nil {
			return nil, err
		}
		if entry.Flags&1 != 0 {
			return nil, errors.New("encrypted extension ZIP is unsupported")
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("extension ZIP contains non-file %q", entry.Name)
		}
		if entry.UncompressedSize64 > maxFileSize {
			return nil, fmt.Errorf("extension file %q exceeds size limit", entry.Name)
		}
		item := entry
		files = append(files, sourceFile{name: entry.Name, size: int64(entry.UncompressedSize64), mode: entry.Mode(), open: item.Open})
	}
	if len(files) > maxFiles {
		return nil, errors.New("extension has too many files")
	}
	return normalizeFiles(files)
}

func normalizeFiles(files []sourceFile) ([]sourceFile, error) {
	if len(files) == 0 {
		return nil, errors.New("extension contains no files")
	}
	// A common exported ZIP contains one wrapping folder; strip it consistently.
	if !hasManifest(files) {
		prefix := strings.SplitN(files[0].name, "/", 2)[0] + "/"
		for _, file := range files {
			if !strings.HasPrefix(file.name, prefix) {
				return nil, errors.New("manifest.json must be at extension root")
			}
		}
		for index := range files {
			files[index].name = strings.TrimPrefix(files[index].name, prefix)
		}
	}
	if !hasManifest(files) {
		return nil, errors.New("manifest.json must be at extension root")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	var total int64
	for i, file := range files {
		if err := safeName(file.name); err != nil {
			return nil, err
		}
		if i > 0 && files[i-1].name == file.name {
			return nil, fmt.Errorf("duplicate extension file %q", file.name)
		}
		if file.size < 0 || file.size > maxFileSize {
			return nil, fmt.Errorf("extension file %q exceeds size limit", file.name)
		}
		total += file.size
		if total > maxArchiveSize {
			return nil, errors.New("extension exceeds size limit")
		}
	}
	return files, nil
}

func hasManifest(files []sourceFile) bool {
	for _, f := range files {
		if f.name == "manifest.json" {
			return true
		}
	}
	return false
}
func safeName(name string) error {
	if name == "" || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.HasPrefix(name, "./") || strings.Contains(name, "//") || strings.Contains(name, ":") {
		return fmt.Errorf("unsafe extension path %q", name)
	}
	return nil
}

func inspectFiles(files []sourceFile) (Description, error) {
	var manifestFile sourceFile
	for _, file := range files {
		if file.name == "manifest.json" {
			manifestFile = file
			break
		}
	}
	if manifestFile.size > maxManifestSize {
		return Description{}, errors.New("extension manifest exceeds size limit")
	}
	reader, err := manifestFile.open()
	if err != nil {
		return Description{}, err
	}
	manifestBytes, err := io.ReadAll(io.LimitReader(reader, maxManifestSize+1))
	reader.Close()
	if err != nil {
		return Description{}, err
	}
	if len(manifestBytes) > maxManifestSize {
		return Description{}, errors.New("extension manifest exceeds size limit")
	}
	var manifest struct {
		ManifestVersion int      `json:"manifest_version"`
		Name            string   `json:"name"`
		Version         string   `json:"version"`
		Key             string   `json:"key"`
		Permissions     []string `json:"permissions"`
		HostPermissions []string `json:"host_permissions"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Description{}, fmt.Errorf("invalid extension manifest: %w", err)
	}
	if manifest.ManifestVersion != 2 && manifest.ManifestVersion != 3 {
		return Description{}, errors.New("unsupported extension manifest version")
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Version) == "" {
		return Description{}, errors.New("extension manifest requires name and version")
	}
	if len(manifest.Name) > 256 || len(manifest.Version) > 64 {
		return Description{}, errors.New("extension name or version exceeds limit")
	}
	if len(manifest.Permissions) > 256 || len(manifest.HostPermissions) > 256 {
		return Description{}, errors.New("extension permission count exceeds limit")
	}
	id := ""
	if manifest.Key != "" {
		key, err := base64.StdEncoding.DecodeString(manifest.Key)
		if err != nil || len(key) == 0 {
			return Description{}, errors.New("extension manifest key is invalid")
		}
		hash := sha256.Sum256(key)
		var value strings.Builder
		for _, b := range hash[:16] {
			value.WriteByte('a' + (b >> 4))
			value.WriteByte('a' + (b & 15))
		}
		id = value.String()
	}
	contentHash := sha256.New()
	var total int64
	for _, file := range files {
		reader, err := file.open()
		if err != nil {
			return Description{}, err
		}
		_, _ = io.WriteString(contentHash, file.name+"\x00")
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(file.size))
		_, _ = contentHash.Write(length[:])
		n, copyErr := io.Copy(contentHash, io.LimitReader(reader, maxFileSize+1))
		closeErr := reader.Close()
		if copyErr != nil {
			return Description{}, copyErr
		}
		if closeErr != nil {
			return Description{}, closeErr
		}
		if n != file.size {
			return Description{}, fmt.Errorf("extension file %q changed or has invalid size", file.name)
		}
		total += n
		if total > maxArchiveSize {
			return Description{}, errors.New("extension exceeds size limit")
		}
	}
	return Description{Name: manifest.Name, Version: manifest.Version, ManifestVersion: manifest.ManifestVersion, ID: id, Permissions: uniqueSorted(manifest.Permissions), HostPermissions: uniqueSorted(manifest.HostPermissions), Files: len(files), Bytes: total, Revision: fmt.Sprintf("sha256:%x", contentHash.Sum(nil))}, nil
}

func uniqueSorted(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
