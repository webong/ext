package bundlecmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cengine/fetch"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
)

type preparedNative struct {
	ID, Root, Relative, Stage, Digest string
	lib                               *library.Described
	afterBackup                       func() error
	afterPublish                      func() error
}

// Native bundle artifacts belong to Ext state, separated by target. The caller
// holds the home writer lock across staging, receipts and publication.
func nativeArtifactRoot(home, target string) (string, error) {
	root, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	relative := "artifacts/native/" + target
	destination, err := nativeDestination(root, relative)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return "", err
	}
	return destination, nil
}

// nativeDestination refuses symlinks in every existing component. The caller
// holds the home writer lock; unrelated writers must not mutate these paths.
func nativeRelative(relative string) error {
	if !library.LocalPath(relative) || !store.SafeArchivePath(relative) {
		return fmt.Errorf("native destination must be a portable artifact-relative path")
	}
	for _, part := range strings.Split(relative, "/") {
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("native destination cannot use hidden artifact directories")
		}
	}
	return nil
}

func nativeDestination(root, relative string) (string, error) {
	if err := nativeRelative(relative); err != nil {
		return "", err
	}
	current := root
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("native destination traverses a non-directory or symlink: %s", current)
		}
	}
	return current, nil
}

// prepareNative reuses fetch's pinned downloader and code generation. Every
// directory stays unpublished until all native and package preparation passes.
func (m Manager) prepareNative(ctx context.Context, lib *library.Described, bundle Bundle, plans []NativePlan, root, stage string) ([]preparedNative, error) {
	prepared := make([]preparedNative, 0, len(plans))
	for i, plan := range plans {
		if _, err := nativeDestination(root, plan.Dir); err != nil {
			return nil, err
		}
		citation, err := library.ParseCitation(bundle.Native[i].Citation)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(stage, fmt.Sprintf("native-%d", i))
		if err := fetch.Fetch(ctx, fetch.Options{Citation: citation, Drivers: bundle.Native[i].Drivers, Targets: []string{bundle.Target}, Dir: dir, Package: plan.Package, Cache: filepath.Join(m.Home, "cache", "native")}); err != nil {
			return nil, fmt.Errorf("prepare native %s: %w", plan.Library, err)
		}
		var digest string
		if err := lib.Library().Call("directory_digest", map[string]any{"root": dir}, &digest); err != nil {
			return nil, err
		}
		n := preparedNative{ID: "native:" + plan.Library, Root: root, Relative: plan.Dir, Stage: dir, Digest: digest, lib: lib}
		if _, err := n.currentMatches(); err != nil {
			return nil, err
		}
		prepared = append(prepared, n)
	}
	return prepared, nil
}
