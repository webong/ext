package pkgcmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
)

// installWithManifest imports a local package under the managed store's layout.
// Only the private copy changes. The normal install path still seals or verifies
// the package, checks upgrades and leaves approval to the user.
func (m Manager) installWithManifest(upgrade bool, dir, manifestPath string, stdout io.Writer) error {
	if manifestPath == "" {
		return fmt.Errorf("--manifest requires a package-relative path")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("--manifest requires a local package directory")
	}
	layout := storelayout.Layout{ManifestPath: manifestPath}
	if _, err := layout.Locate(dir); err != nil {
		return fmt.Errorf("manifest %q: %w", manifestPath, err)
	}
	managed := m.Store.Layout().ManifestPath
	if manifestPath == managed {
		return m.install(upgrade, dir, stdout)
	}
	source, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer source.Close()
	stage, err := os.MkdirTemp("", "ext-manifest-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.CopyFS(stage, source.FS()); err != nil {
		return err
	}
	if _, err := layout.Locate(stage); err != nil {
		return err
	}
	staged, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer staged.Close()
	target := filepath.FromSlash(managed)
	if _, err := staged.Lstat(target); err == nil {
		return fmt.Errorf("package already contains %q, the managed manifest path", managed)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := staged.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := staged.Rename(filepath.FromSlash(manifestPath), target); err != nil {
		return err
	}
	return m.install(upgrade, stage, stdout)
}
