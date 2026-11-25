// Package storelayout describes how an application locates a manifest inside a
// package. Paths are relative to the package root, including inside archives.
package storelayout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webong/ext/pkg/ext-go/engine/plugin"
)

const (
	// LegacyManifest is the filename used by callers without an explicit layout.
	LegacyManifest = "plugin.json"
	// ExtManifest is the filename used by Ext-managed packages.
	ExtManifest = "ext.json"
)

// Layout is an application's package layout. FallbackPath is read only when
// ManifestPath is absent. An invalid or unreadable primary never falls back.
type Layout struct {
	ManifestPath string `json:"manifest_path"`
	FallbackPath string `json:"fallback_path,omitempty"`
}

// Ext returns the layout of Ext installations and their historical packages.
func Ext() Layout {
	return Layout{ManifestPath: ExtManifest, FallbackPath: LegacyManifest}
}

// Normalize validates paths and provides the legacy convention for a zero layout.
func (l Layout) Normalize() (Layout, error) {
	if l == (Layout{}) {
		l.ManifestPath = LegacyManifest
	}
	if !plugin.PortablePath(l.ManifestPath) ||
		l.FallbackPath != "" && (!plugin.PortablePath(l.FallbackPath) || strings.EqualFold(l.ManifestPath, l.FallbackPath)) {
		return Layout{}, fmt.Errorf("%w: invalid package manifest layout", plugin.ErrInvalid)
	}
	return l, nil
}

// Locate returns the selected package-relative path, refusing links and
// special files. Callers must protect the source while using the result.
func (l Layout) Locate(root string) (string, error) {
	l, err := l.Normalize()
	if err != nil {
		return "", err
	}
	err = regularFile(root, l.ManifestPath)
	if errors.Is(err, os.ErrNotExist) && l.FallbackPath != "" {
		if err := regularFile(root, l.FallbackPath); err != nil {
			return "", err
		}
		return l.FallbackPath, nil
	}
	if err != nil {
		return "", err
	}
	return l.ManifestPath, nil
}

func regularFile(root, relative string) error {
	current := root
	parts := strings.Split(relative, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("%w: invalid package manifest path %q", plugin.ErrInvalid, relative)
		}
	}
	return nil
}

// Reserves reports whether an artifact uses the selected manifest itself, or
// includes it as a member of an artifact tree.
func Reserves(manifestPath, artifactPath string, tree bool) bool {
	manifest := strings.ToLower(manifestPath)
	artifact := strings.ToLower(artifactPath)
	return manifest == artifact || tree && strings.HasPrefix(manifest, artifact+"/")
}
