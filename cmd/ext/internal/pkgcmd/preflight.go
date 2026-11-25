package pkgcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// checkReplacement protects installed consumers before a provider is replaced.
// New unresolved packages may still be staged; trust and invocation enforce
// their requirements separately.
func (m Manager) checkReplacement(dir string) error {
	path := filepath.Join(dir, store.ManifestName)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return fmt.Errorf("candidate manifest is not a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	f.Close()
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return fmt.Errorf("candidate manifest exceeds limit")
	}
	var candidate packagekit.Manifest
	if err := json.Unmarshal(data, &candidate); err != nil {
		return err
	}
	installed, err := m.Store.List()
	if err != nil {
		return err
	}
	replacement := false
	for _, p := range installed {
		if p.Name() == candidate.Name() {
			replacement = true
		}
	}
	if !replacement {
		return nil
	}
	for _, p := range installed {
		if !hasRequirements(p.Manifest) {
			continue
		}
		report, err := m.resolvePlan("", dir)
		if err != nil {
			return err
		}
		if report.Status != "ok" {
			return fmt.Errorf("package change leaves unresolved dependencies (%s); inspect ext package plan --with %s", report.Status, dir)
		}
		return nil
	}
	return nil
}

func (m Manager) installArchive(ctx context.Context, archive store.Archive) (store.Installed, error) {
	work, err := os.MkdirTemp("", "ext-package-preflight-")
	if err != nil {
		return store.Installed{}, err
	}
	defer os.RemoveAll(work)
	path := archive.Source
	if strings.HasPrefix(path, "https://") {
		path = filepath.Join(work, "archive")
		if err := store.Download(ctx, nil, archive.Source, path, store.DefaultMaxArchiveBytes); err != nil {
			return store.Installed{}, err
		}
	} else if strings.Contains(path, "://") {
		return store.Installed{}, fmt.Errorf("a remote archive requires an HTTPS URL")
	}
	root, err := store.ExtractArchive(path, archive.SHA256, filepath.Join(work, "package"), store.Limits{})
	if err != nil {
		return store.Installed{}, err
	}
	if err := m.checkReplacement(root); err != nil {
		return store.Installed{}, err
	}
	// Publication checks the pin again; a changed local archive cannot bypass the
	// manifest preview. Downloaded bytes are reused, avoiding a second request.
	archive.Source = path
	return m.Store.InstallArchive(ctx, archive, store.ArchiveOptions{Copy: store.CopyOptions{PreserveExecutableArtifacts: true}})
}
