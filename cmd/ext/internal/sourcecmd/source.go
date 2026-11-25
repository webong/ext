// Package sourcecmd manages CLI catalog configuration. Catalog semantics belong
// to the described C engine; this package owns downloading and local settings.
package sourcecmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const maxIndexBytes = 1 << 20

var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type Source struct {
	Location string          `json:"location"`
	SHA256   string          `json:"sha256"`
	Index    json.RawMessage `json:"index"`
}
type document struct {
	Version int               `json:"version"`
	Sources map[string]Source `json:"sources"`
}
type Manager struct {
	Home string
	// transport is a test seam; production uses the system TLS roots.
	transport http.RoundTripper
}

const usage = "usage: ext source add <name> <index-path-or-https-url> | list | inspect <name> | refresh <name> | remove <name> | import-catalog <directory>"

func readFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("not a bounded regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("file exceeds limit")
	}
	return data, err
}
func (m Manager) load() (document, error) {
	d := document{Version: 1, Sources: map[string]Source{}}
	data, err := readFile(filepath.Join(m.Home, "sources.json"), 16<<20)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return d, fmt.Errorf("trailing source configuration")
	}
	if d.Version != 1 || d.Sources == nil || len(d.Sources) > 256 {
		return d, fmt.Errorf("invalid source configuration")
	}
	for name, s := range d.Sources {
		if !validName.MatchString(name) {
			return d, fmt.Errorf("invalid source name")
		}
		canonical, err := json.Marshal(s.Index)
		if err != nil {
			return d, err
		}
		sum := sha256.Sum256(canonical)
		if hex.EncodeToString(sum[:]) != s.SHA256 {
			return d, fmt.Errorf("source %s snapshot digest changed", name)
		}
	}
	return d, nil
}
func (m Manager) save(d document) error {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("source configuration exceeds 16 MiB")
	}
	f, err := os.CreateTemp(m.Home, ".sources-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(m.Home, "sources.json"))
}
func location(value string) (string, error) {
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return "", fmt.Errorf("source requires HTTPS without credentials, query or fragment")
		}
		return u.String(), nil
	}
	return filepath.Abs(value)
}
func (m Manager) fetch(where string) ([]byte, error) {
	if strings.HasPrefix(where, "https://") {
		dir, err := os.MkdirTemp("", "ext-source-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "index.json")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		client := store.NewDownloadClient()
		client.Transport = m.transport
		redirect := client.CheckRedirect
		origin, _ := url.Parse(where)
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if err := redirect(req, via); err != nil {
				return err
			}
			if _, err := location(req.URL.String()); err != nil {
				return err
			}
			if req.URL.Host != origin.Host {
				return fmt.Errorf("source redirect changes origin")
			}
			return nil
		}
		if err := store.Download(ctx, client, where, path, maxIndexBytes); err != nil {
			return nil, err
		}
		return readFile(path, maxIndexBytes)
	}
	return readFile(where, maxIndexBytes)
}
func (m Manager) snapshot(where string) (Source, error) {
	data, err := m.fetch(where)
	if err != nil {
		return Source{}, err
	}
	lib, err := nativeplugin.Open()
	if err != nil {
		return Source{}, err
	}
	var index json.RawMessage
	if err := lib.Library().Call("catalog.validate", json.RawMessage(data), &index); err != nil {
		return Source{}, err
	}
	index, err = json.Marshal(index)
	if err != nil {
		return Source{}, err
	}
	sum := sha256.Sum256(index)
	return Source{Location: where, SHA256: hex.EncodeToString(sum[:]), Index: index}, nil
}
func identity(s Source) string {
	var index struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(s.Index, &index)
	return index.Name
}

func (m Manager) Run(args []string, out, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	op := args[0]
	if op == "import-catalog" && len(args) == 2 {
		if err := m.importCatalog(args[1], out); err != nil {
			fmt.Fprintln(stderr, "ext: source:", err)
			return 1
		}
		return 0
	}
	want := 2
	if op == "add" {
		want = 3
	}
	if op == "list" {
		want = 1
	}
	if len(args) != want || (op != "add" && op != "list" && op != "inspect" && op != "refresh" && op != "remove") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if want > 1 && !validName.MatchString(args[1]) {
		fmt.Fprintln(stderr, "invalid source name")
		return 2
	}
	err := m.run(op, args[1:], out)
	if err != nil {
		fmt.Fprintln(stderr, "ext: source:", err)
		return 1
	}
	return 0
}
func (m Manager) run(op string, args []string, out io.Writer) error {
	if op == "add" || op == "refresh" || op == "remove" {
		if err := os.MkdirAll(m.Home, 0700); err != nil {
			return err
		}
		lock := filepath.Join(m.Home, ".sources.lock")
		if err := os.Mkdir(lock, 0700); err != nil {
			return fmt.Errorf("source writer lock: %w", err)
		}
		defer os.Remove(lock)
	}
	d, err := m.load()
	if err != nil {
		return err
	}
	if op == "list" {
		names := make([]string, 0, len(d.Sources))
		for name := range d.Sources {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			s := d.Sources[name]
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", name, identity(s), s.SHA256, s.Location)
		}
		return nil
	}
	name := args[0]
	old, exists := d.Sources[name]
	if op == "add" && exists {
		return fmt.Errorf("source %s already exists", name)
	}
	if op != "add" && !exists {
		return fmt.Errorf("source %s not found", name)
	}
	switch op {
	case "inspect":
		return json.NewEncoder(out).Encode(old)
	case "remove":
		delete(d.Sources, name)
	case "add", "refresh":
		where := old.Location
		if op == "add" {
			where, err = location(args[1])
			if err != nil {
				return err
			}
			if len(d.Sources) >= 256 {
				return fmt.Errorf("source limit reached")
			}
		}
		next, err := m.snapshot(where)
		if err != nil {
			return err
		}
		if exists && identity(next) != identity(old) {
			return fmt.Errorf("source package identity changed; remove and add explicitly")
		}
		d.Sources[name] = next
	}
	if err := m.save(d); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s source %s\n", op, name)
	return nil
}

// Resolve selects from the reviewed cached catalog; it never refreshes or trusts.
func (m Manager) Resolve(name string) (store.Archive, error) {
	d, err := m.load()
	if err != nil {
		return store.Archive{}, err
	}
	s, ok := d.Sources[name]
	if !ok {
		return store.Archive{}, fmt.Errorf("source %s not found", name)
	}
	lib, err := nativeplugin.Open()
	if err != nil {
		return store.Archive{}, err
	}
	var selected struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	if err := lib.Library().Call("catalog.select", map[string]any{"index": s.Index, "target": runtime.GOOS + "/" + runtime.GOARCH}, &selected); err != nil {
		return store.Archive{}, err
	}
	path, err := archiveLocation(s.Location, selected.URL)
	if err != nil {
		return store.Archive{}, err
	}

	return store.Archive{Source: path, SHA256: selected.SHA256, ID: identity(s)}, nil
}

func archiveLocation(source, archive string) (string, error) {
	ref, err := url.Parse(archive)
	if err != nil {
		return "", err
	}
	var path string
	if strings.HasPrefix(source, "https://") {
		base, _ := url.Parse(source)
		resolved := base.ResolveReference(ref)
		if resolved.Scheme != "https" || resolved.Host != base.Host || resolved.User != nil || resolved.Fragment != "" || resolved.RawQuery != "" {
			return "", errors.New("archive URL must remain on the source HTTPS origin without credentials, query or fragment")
		}
		path = resolved.String()
	} else {
		if ref.IsAbs() || ref.Host != "" || ref.RawQuery != "" || ref.Fragment != "" || strings.ContainsAny(archive, "\\%") || filepath.IsAbs(archive) {
			return "", errors.New("local archive must be a relative path")
		}
		clean := filepath.Clean(archive)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", errors.New("archive escapes source directory")
		}
		path = filepath.Join(filepath.Dir(source), clean)
	}
	return path, nil
}
