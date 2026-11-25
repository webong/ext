// Package bundlecmd composes existing pinned package archives. Resolution,
// package verification and publication are served by the canonical C engine.
package bundlecmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/cmd/ext/internal/packagelock"
	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	"github.com/webong/ext/pkg/ext-go/cengine/libcmd"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

type Archive struct {
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}
type Item struct {
	Manifest json.RawMessage `json:"manifest"`
	Archive  Archive         `json:"archive"`
}
type Bundle struct {
	APIVersion string      `json:"apiVersion"`
	Target     string      `json:"target"`
	Packages   []Item      `json:"packages"`
	Native     []NativeJob `json:"native,omitempty"`
}
type Manager struct{ Home string }
type journal struct {
	Digest       string   `json:"digest"`
	State        string   `json:"state"`
	Completed    []string `json:"completed"`
	Error        string   `json:"error,omitempty"`
	ArtifactRoot string   `json:"artifactRoot,omitempty"`
}

const usage = "usage: ext bundle plan|inspect|install <bundle.json> \n       ext bundle fetch ... | bind ..."

func (m Manager) Run(args []string, out, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "fetch" || args[0] == "bind") {
		return libcmd.Run(args, out, stderr)
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, usage)
		return libcmd.Run([]string{"help"}, out, stderr)
	}
	if len(args) != 2 || (args[0] != "plan" && args[0] != "inspect" && args[0] != "install") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if err := m.run(args[0], args[1], out); err != nil {
		fmt.Fprintln(stderr, "ext: bundle:", err)
		return 1
	}
	return 0
}
func read(path string, limit int64) ([]byte, error) {
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
func manifestID(raw json.RawMessage) (string, error) {
	var m struct {
		Descriptor struct {
			Identity plugin.Identity `json:"identity"`
		} `json:"descriptor"`
	}
	err := json.Unmarshal(raw, &m)
	return m.Descriptor.Identity.ID, err
}
func sameJSON(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, error) {
		var v any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		err := d.Decode(&v)
		return v, err
	}
	av, ae := decode(a)
	bv, be := decode(b)
	return ae == nil && be == nil && reflect.DeepEqual(av, bv)
}
func archivePath(bundleFile, source string) (string, error) {
	u, err := url.Parse(source)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", fmt.Errorf("archive requires HTTPS without credentials, query or fragment")
		}
		return u.String(), nil
	}
	if u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(source, "\\%") || filepath.IsAbs(source) {
		return "", fmt.Errorf("archive must be relative to the bundle or HTTPS")
	}
	clean := filepath.Clean(source)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive escapes bundle directory")
	}
	return filepath.Join(filepath.Dir(bundleFile), clean), nil
}
func copyArchive(ctx context.Context, source, destination string) error {
	if strings.HasPrefix(source, "https://") {
		return store.Download(ctx, nil, source, destination, store.DefaultMaxArchiveBytes)
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > store.DefaultMaxArchiveBytes {
		return fmt.Errorf("invalid archive file")
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, store.DefaultMaxArchiveBytes+1))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if n > store.DefaultMaxArchiveBytes {
		return fmt.Errorf("archive exceeds limit")
	}
	return closeErr
}
func (m Manager) run(op, file string, out io.Writer) error {
	file, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	data, err := read(file, 4<<20)
	if err != nil {
		return err
	}
	lib, err := nativeplugin.Open()
	if err != nil {
		return err
	}
	var plan packagekit.Plan
	if err := lib.Library().Call("bundle.plan", json.RawMessage(data), &plan); err != nil {
		return err
	}
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return err
	}
	bundleGraph := make([]packagekit.Manifest, len(bundle.Packages))
	for i, item := range bundle.Packages {
		if err := json.Unmarshal(item.Manifest, &bundleGraph[i]); err != nil {
			return err
		}
	}
	bundleGraph, err = adapter.ProjectDependenciesFor(bundleGraph, adapter.Target{OS: strings.SplitN(bundle.Target, "/", 2)[0], Arch: strings.SplitN(bundle.Target, "/", 2)[1]})
	if err != nil {
		return fmt.Errorf("bundle adapter dependencies: %w", err)
	}
	if err := lib.Library().Call("package.resolve_for", map[string]any{"packages": bundleGraph, "os": strings.SplitN(bundle.Target, "/", 2)[0], "arch": strings.SplitN(bundle.Target, "/", 2)[1]}, &plan); err != nil {
		return err
	}
	nativePlans, err := planNative(bundle)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	digest := hex.EncodeToString(sum[:])
	if op != "install" {
		return json.NewEncoder(out).Encode(struct {
			Digest string          `json:"digest"`
			Bundle Bundle          `json:"bundle"`
			Plan   packagekit.Plan `json:"plan"`
			Native []NativePlan    `json:"native,omitempty"`
		}{digest, bundle, plan, nativePlans})
	}
	if len(bundle.Packages) > 0 && bundle.Target != runtime.GOOS+"/"+runtime.GOARCH {
		return fmt.Errorf("bundle targets %s, host is %s/%s", bundle.Target, runtime.GOOS, runtime.GOARCH)
	}

	release, err := packagelock.Acquire(m.Home)
	if err != nil {
		return err
	}
	defer release()
	if err := pkgcmd.RequireMigration(m.Home); err != nil {
		return err
	}
	artifactRoot := ""
	stageRoot := ""
	if len(nativePlans) > 0 {
		artifactRoot, err = nativeArtifactRoot(m.Home, bundle.Target)
		if err != nil {
			return err
		}
		stageRoot = artifactRoot
	}
	stage, err := os.MkdirTemp(stageRoot, ".ext-bundle-stage-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	staged, err := lib.Open("store", map[string]any{"root": filepath.Join(stage, "packages")}, nil)
	if err != nil {
		return err
	}
	defer staged.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	natives, err := m.prepareNative(ctx, lib, bundle, nativePlans, artifactRoot, stage)
	if err != nil {
		return err
	}
	items := map[string]Item{}
	archives := map[string]string{}
	// Verify every archive and its exact pinned manifest before publishing any.
	for i, item := range bundle.Packages {
		id, err := manifestID(item.Manifest)
		if err != nil {
			return err
		}
		items[id] = item
		source, err := archivePath(file, item.Archive.Source)
		if err != nil {
			return err
		}
		local := filepath.Join(stage, fmt.Sprintf("archive-%d", i))
		if err := copyArchive(ctx, source, local); err != nil {
			return fmt.Errorf("prepare %s: %w", id, err)
		}
		var actual json.RawMessage
		if err := staged.Call("install_archive", map[string]any{"archive": local, "sha256": item.Archive.SHA256, "id": id, "preserve_executable": true}, &actual); err != nil {
			return fmt.Errorf("prepare %s: %w", id, err)
		}
		if !sameJSON(actual, item.Manifest) {
			return fmt.Errorf("archive manifest differs from pinned manifest for %s", id)
		}
		archives[id] = local
	}
	installed, err := lib.Open("store", map[string]any{"root": filepath.Join(m.Home, "packages")}, nil)
	if err != nil {
		return err
	}
	defer installed.Close()
	var rows []struct {
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := installed.Call("list", nil, &rows); err != nil {
		return err
	}
	projected := []json.RawMessage{}
	for _, row := range rows {
		id, err := manifestID(row.Manifest)
		if err != nil {
			return err
		}
		if _, replaced := items[id]; !replaced {
			projected = append(projected, row.Manifest)
		}
	}
	for _, item := range bundle.Packages {
		projected = append(projected, item.Manifest)
	}
	graph := make([]packagekit.Manifest, len(projected))
	for i, raw := range projected {
		if err := json.Unmarshal(raw, &graph[i]); err != nil {
			return err
		}
	}
	graph, err = adapter.ProjectDependencies(graph, runtime.GOOS)
	if err != nil {
		return fmt.Errorf("bundle adapter dependencies: %w", err)
	}
	var resolved packagekit.Plan
	if err := lib.Library().Call("package.resolve_for", map[string]any{"packages": graph, "os": runtime.GOOS, "arch": runtime.GOARCH}, &resolved); err != nil {
		return fmt.Errorf("bundle conflicts with installed dependencies: %w", err)
	}
	order := make([]plugin.Identity, 0, len(natives)+len(plan.Order))
	nativeByID := map[string]preparedNative{}
	for _, n := range natives {
		order = append(order, plugin.Identity{ID: n.ID})
		nativeByID[n.ID] = n
	}
	for _, identity := range resolved.Order {
		if _, ok := items[identity.ID]; ok {
			order = append(order, identity)
		}
	}
	return m.publishRoot(order, digest, artifactRoot, func(id string) error {
		if n, ok := nativeByID[id]; ok {
			return n.publish()
		}
		item := items[id]
		return installed.Call("install_archive", map[string]any{"archive": archives[id], "sha256": item.Archive.SHA256, "id": id, "preserve_executable": true}, nil)
	}, out)
}

// publish records progress but never treats the journal as proof that installed
// bytes are valid. A retry asks the store to reconcile every pinned package.
func (m Manager) publish(order []plugin.Identity, digest string, install func(string) error, out io.Writer) error {
	return m.publishRoot(order, digest, "", install, out)
}
func (m Manager) publishRoot(order []plugin.Identity, digest, artifactRoot string, install func(string) error, out io.Writer) error {
	progress := journal{Digest: digest, ArtifactRoot: artifactRoot, State: "installing", Completed: []string{}}
	if err := m.saveJournal(progress); err != nil {
		return err
	}
	for _, identity := range order {
		if err := install(identity.ID); err != nil {
			progress.State = "partial"
			progress.Error = err.Error()
			saveErr := m.saveJournal(progress)
			return fmt.Errorf("install %s after %d items: %w (journal: %v); rerun the same bundle to resume", identity.ID, len(progress.Completed), err, saveErr)
		}
		progress.Completed = append(progress.Completed, identity.ID)
		if err := m.saveJournal(progress); err != nil {
			return fmt.Errorf("item %s published but journal failed: %w; rerun to reconcile", identity.ID, err)
		}
	}
	progress.State = "complete"
	if err := m.saveJournal(progress); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(progress)
}

func (m Manager) saveJournal(j journal) error {
	journalID := j.Digest
	if j.ArtifactRoot != "" {
		sum := sha256.Sum256([]byte(j.ArtifactRoot))
		journalID += "-" + hex.EncodeToString(sum[:])
	}
	dir := filepath.Join(m.Home, "bundles")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".journal-*")
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
	return os.Rename(f.Name(), filepath.Join(dir, journalID+".json"))
}
