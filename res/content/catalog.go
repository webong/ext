package content

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxCatalogItemBytes int64 = 4 << 30
const maxRecordBytes = 64 << 10

var ErrCatalogInvalid = errors.New("invalid content catalog entry")
var ErrCatalogNotFound = errors.New("content item not found")

// Record describes one immutable, locally stored file. ID is a random
// identifier; SHA256 identifies the exact content bytes.
type Record struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MediaType  string `json:"mediaType"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	ImportedAt string `json:"importedAt"`
}

// Catalog owns a caller-selected local root. Items are atomically published
// as directories containing a record and a content file.
type Catalog struct {
	root *os.Root
	path string
}

// InitCatalog creates a new protected catalog root and opens it. The parent
// directory must already exist; an existing root is never overwritten.
func InitCatalog(path string) (*Catalog, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, ErrCatalogInvalid
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, err
	}
	return OpenCatalog(path)
}

// OpenCatalog opens an existing protected root without following a root link.
func OpenCatalog(path string) (*Catalog, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, ErrCatalogInvalid
	}
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || os.PathSeparator == '/' && info.Mode().Perm()&0o022 != 0 {
		return nil, ErrCatalogInvalid
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, ErrCatalogInvalid
	}
	c := &Catalog{root: root, path: path}
	if err := c.ensureItems(false); err != nil && !errors.Is(err, fs.ErrNotExist) {
		root.Close()
		return nil, err
	}
	return c, nil
}

func (c *Catalog) Path() string { return c.path }
func (c *Catalog) Close() error { return c.root.Close() }

func (c *Catalog) ensureItems(create bool) error {
	if create {
		if err := c.root.Mkdir("items", 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	info, err := c.root.Lstat("items")
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrCatalogInvalid
	}
	return nil
}

// Import streams a regular source file into a private staging directory,
// hashes it, then publishes the complete item with one directory rename.
func (c *Catalog) Import(ctx context.Context, source string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if !filepath.IsAbs(source) || !validName(filepath.Base(source)) {
		return Record{}, ErrCatalogInvalid
	}
	info, err := os.Lstat(source)
	if err != nil {
		return Record{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxCatalogItemBytes {
		return Record{}, ErrCatalogInvalid
	}
	input, err := os.Open(source)
	if err != nil {
		return Record{}, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Record{}, ErrCatalogInvalid
	}
	if err := c.ensureItems(true); err != nil {
		return Record{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Record{}, err
	}
	id := hex.EncodeToString(random[:])
	stage := ".import-" + id
	if err := c.root.Mkdir(stage, 0o700); err != nil {
		return Record{}, err
	}
	defer c.root.RemoveAll(stage)
	output, err := c.root.OpenFile(stage+"/content", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Record{}, err
	}
	h := sha256.New()
	first := make([]byte, 0, 512)
	buffer := make([]byte, 64<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			output.Close()
			return Record{}, err
		}
		n, readErr := input.Read(buffer)
		if n > 0 {
			if size+int64(n) > MaxCatalogItemBytes {
				output.Close()
				return Record{}, ErrCatalogInvalid
			}
			if len(first) < 512 {
				first = append(first, buffer[:min(n, 512-len(first))]...)
			}
			if _, err := output.Write(buffer[:n]); err != nil {
				output.Close()
				return Record{}, err
			}
			_, _ = h.Write(buffer[:n])
			size += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			output.Close()
			return Record{}, readErr
		}
	}
	if err := output.Sync(); err != nil {
		output.Close()
		return Record{}, err
	}
	if err := output.Close(); err != nil {
		return Record{}, err
	}
	record := Record{ID: id, Name: filepath.Base(source), MediaType: http.DetectContentType(first), Size: size,
		SHA256: hex.EncodeToString(h.Sum(nil)), ImportedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	data, err := json.Marshal(record)
	if err != nil {
		return Record{}, err
	}
	meta, err := c.root.OpenFile(stage+"/record.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Record{}, err
	}
	if _, err := meta.Write(data); err != nil {
		meta.Close()
		return Record{}, err
	}
	if err := meta.Sync(); err != nil {
		meta.Close()
		return Record{}, err
	}
	if err := meta.Close(); err != nil {
		return Record{}, err
	}
	if err := c.root.Rename(stage, "items/"+id); err != nil {
		return Record{}, err
	}
	return record, nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Inspect rehashes the stored bytes before returning metadata.
func (c *Catalog) Inspect(ctx context.Context, id string) (Record, error) {
	if !validID(id) {
		return Record{}, ErrCatalogInvalid
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if err := c.ensureItems(false); errors.Is(err, fs.ErrNotExist) {
		return Record{}, ErrCatalogNotFound
	} else if err != nil {
		return Record{}, err
	}
	base := "items/" + id
	info, err := c.root.Lstat(base)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, ErrCatalogNotFound
	}
	if err != nil {
		return Record{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Record{}, ErrCatalogInvalid
	}
	entries, err := fs.ReadDir(c.root.FS(), base)
	if err != nil || len(entries) != 2 || entries[0].Name() != "content" || entries[1].Name() != "record.json" {
		return Record{}, ErrCatalogInvalid
	}
	metaInfo, err := c.root.Lstat(base + "/record.json")
	if err != nil || !metaInfo.Mode().IsRegular() {
		return Record{}, ErrCatalogInvalid
	}
	meta, err := c.root.Open(base + "/record.json")
	if err != nil {
		return Record{}, err
	}
	openedMeta, err := meta.Stat()
	if err != nil || !os.SameFile(metaInfo, openedMeta) {
		meta.Close()
		return Record{}, ErrCatalogInvalid
	}
	data, err := io.ReadAll(io.LimitReader(meta, maxRecordBytes+1))
	meta.Close()
	if err != nil || len(data) > maxRecordBytes {
		return Record{}, ErrCatalogInvalid
	}
	var record Record
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || record.ID != id || !validName(record.Name) ||
		record.Size < 0 || record.Size > MaxCatalogItemBytes || len(record.SHA256) != 64 ||
		record.MediaType == "" || len(record.MediaType) > 255 {
		return Record{}, ErrCatalogInvalid
	}
	if _, err := hex.DecodeString(record.SHA256); err != nil || strings.ToLower(record.SHA256) != record.SHA256 {
		return Record{}, ErrCatalogInvalid
	}
	if _, err := time.Parse(time.RFC3339Nano, record.ImportedAt); err != nil {
		return Record{}, ErrCatalogInvalid
	}
	contentInfo, err := c.root.Lstat(base + "/content")
	if err != nil || !contentInfo.Mode().IsRegular() || contentInfo.Size() != record.Size {
		return Record{}, ErrCatalogInvalid
	}
	f, err := c.root.Open(base + "/content")
	if err != nil {
		return Record{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(contentInfo, opened) {
		return Record{}, ErrCatalogInvalid
	}
	h := sha256.New()
	first := make([]byte, 0, 512)
	buffer := make([]byte, 64<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return Record{}, err
		}
		n, readErr := f.Read(buffer)
		if n > 0 {
			if size+int64(n) > MaxCatalogItemBytes {
				return Record{}, ErrCatalogInvalid
			}
			if len(first) < 512 {
				first = append(first, buffer[:min(n, 512-len(first))]...)
			}
			_, _ = h.Write(buffer[:n])
			size += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Record{}, readErr
		}
	}
	if size != record.Size || hex.EncodeToString(h.Sum(nil)) != record.SHA256 ||
		http.DetectContentType(first) != record.MediaType {
		return Record{}, ErrCatalogInvalid
	}
	return record, nil
}

// List verifies every item and returns records in stable import order.
func (c *Catalog) List(ctx context.Context) ([]Record, error) {
	if err := c.ensureItems(false); errors.Is(err, fs.ErrNotExist) {
		return []Record{}, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(c.root.FS(), "items")
	if err != nil {
		return nil, err
	}
	items := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, err := c.Inspect(ctx, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", entry.Name(), err)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ImportedAt == items[j].ImportedAt {
			return items[i].ID < items[j].ID
		}
		return items[i].ImportedAt < items[j].ImportedAt
	})
	return items, nil
}

// Remove deletes a caller-named item, including one whose bytes are damaged.
func (c *Catalog) Remove(id string) error {
	if !validID(id) {
		return ErrCatalogInvalid
	}
	if err := c.ensureItems(false); errors.Is(err, fs.ErrNotExist) {
		return ErrCatalogNotFound
	} else if err != nil {
		return err
	}
	info, err := c.root.Lstat("items/" + id)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrCatalogNotFound
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrCatalogInvalid
	}
	return c.root.RemoveAll("items/" + id)
}
