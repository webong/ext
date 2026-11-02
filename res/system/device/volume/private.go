// Package volume provides bounded snapshot and restore mechanics for a
// caller-selected local volume or directory. Identity, authorization,
// retention, and transport policy belong to the caller.
package volume

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	// ErrUnsafePath reports a link, reparse point, non-directory, or broad
	// target the caller did not intend to create or delete.
	ErrUnsafePath = errors.New("unsafe private volume path")
	// ErrStale reports a volume whose native identity no longer matches the
	// inspected directory.
	ErrStale = errors.New("private volume identity changed")
	// ErrLimit reports content beyond a caller-selected creation or removal
	// bound.
	ErrLimit = errors.New("private volume limit exceeded")
	// ErrInvalid reports a malformed path or volume value.
	ErrInvalid = errors.New("invalid private volume")
)

const (
	// DefaultMaxEntries bounds the files and directories removal may traverse.
	DefaultMaxEntries = 10_000
	// MaxEntries is the library ceiling for MaxEntries.
	MaxEntries = 1_000_000
	// DefaultMaxBytes bounds the content bytes removal may traverse.
	DefaultMaxBytes int64 = 1 << 30
	// MaxBytes is the library ceiling for MaxBytes.
	MaxBytes int64 = 1 << 40
	// DefaultMaxDepth bounds the number of directory components a path may add.
	DefaultMaxDepth = 32
	// MaxDepth is the library ceiling for MaxDepth.
	MaxDepth     = 128
	maxComponent = 255
	maxPathBytes = 4096
)

// PrivateLimits bound creation and removal. Zero values select the defaults.
type PrivateLimits struct {
	MaxEntries int
	MaxBytes   int64
	MaxDepth   int
}

func (l PrivateLimits) normalize() (PrivateLimits, error) {
	if l.MaxEntries == 0 {
		l.MaxEntries = DefaultMaxEntries
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxDepth == 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	if l.MaxEntries < 1 || l.MaxEntries > MaxEntries {
		return l, fmt.Errorf("%w: entry bound must be between 1 and %d", ErrInvalid, MaxEntries)
	}
	if l.MaxBytes < 1 || l.MaxBytes > MaxBytes {
		return l, fmt.Errorf("%w: byte bound must be between 1 and %d", ErrInvalid, MaxBytes)
	}
	if l.MaxDepth < 1 || l.MaxDepth > MaxDepth {
		return l, fmt.Errorf("%w: depth bound must be between 1 and %d", ErrInvalid, MaxDepth)
	}
	return l, nil
}

// PrivateVolume is an inspected, caller-selected private directory. Identity is
// an opaque digest for reporting; native same-file checks pin the handle.
// A PrivateVolume is only usable for removal when produced by InspectPrivate or
// CreatePrivate.
type PrivateVolume struct {
	Path     string
	Identity string
	Created  bool
	Entries  int
	Bytes    int64
	info     os.FileInfo
}

// CreatePrivate creates a caller-selected private directory with 0700
// permissions and missing parents. The library never selects the path and never
// authorizes deletion; the caller does both.
func CreatePrivate(ctx context.Context, path string, limits PrivateLimits) (PrivateVolume, error) {
	limits, err := limits.normalize()
	if err != nil {
		return PrivateVolume{}, err
	}
	absolute, err := privatePath(path, limits)
	if err != nil {
		return PrivateVolume{}, err
	}
	if err = ctx.Err(); err != nil {
		return PrivateVolume{}, err
	}
	if err = rejectUnsafeComponents(absolute, limits); err != nil {
		return PrivateVolume{}, err
	}
	_, statErr := os.Lstat(absolute)
	if err = os.MkdirAll(absolute, 0o700); err != nil {
		return PrivateVolume{}, fmt.Errorf("create private volume: %w", err)
	}
	// Recheck after the mutation: a component may have been replaced by a link.
	volume, err := InspectPrivate(ctx, absolute, limits)
	if err != nil {
		return PrivateVolume{}, err
	}
	volume.Created = errors.Is(statErr, os.ErrNotExist)
	return volume, nil
}

// InspectPrivate validates and counts an existing private directory without
// following links. It refuses a directory readable or writable by another user.
func InspectPrivate(ctx context.Context, path string, limits PrivateLimits) (PrivateVolume, error) {
	limits, err := limits.normalize()
	if err != nil {
		return PrivateVolume{}, err
	}
	absolute, err := privatePath(path, limits)
	if err != nil {
		return PrivateVolume{}, err
	}
	if err = ctx.Err(); err != nil {
		return PrivateVolume{}, err
	}
	if err = rejectUnsafeComponents(absolute, limits); err != nil {
		return PrivateVolume{}, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return PrivateVolume{}, fmt.Errorf("inspect private volume: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return PrivateVolume{}, fmt.Errorf("%w: private volume is not a directory", ErrUnsafePath)
	}
	if isReparsePoint(absolute, info) {
		return PrivateVolume{}, fmt.Errorf("%w: private volume is a reparse point", ErrUnsafePath)
	}
	if err := verifyPrivatePermissions(absolute, info); err != nil {
		return PrivateVolume{}, err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return PrivateVolume{}, fmt.Errorf("inspect private volume: %w", err)
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return PrivateVolume{}, fmt.Errorf("%w: private volume changed during inspection", ErrStale)
	}
	entries, bytes, err := measurePrivate(ctx, root, limits)
	if err != nil {
		return PrivateVolume{}, err
	}
	return PrivateVolume{
		Path: absolute, Identity: privateIdentity(absolute, info),
		Entries: entries, Bytes: bytes, info: info,
	}, nil
}

// RemovePrivate removes exactly the inspected directory after revalidating its
// native identity and bounded contents. It refuses links, reparse points, broad
// targets, and a directory replaced since inspection. The caller supplies the
// traversal bounds and authorizes deletion; the library does neither.
func RemovePrivate(ctx context.Context, volume PrivateVolume, limits PrivateLimits) error {
	if volume.info == nil {
		return fmt.Errorf("%w: removal requires an inspected volume", ErrInvalid)
	}
	limits, err := limits.normalize()
	if err != nil {
		return err
	}
	absolute, err := privatePath(volume.Path, limits)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = rejectUnsafeComponents(absolute, limits); err != nil {
		return err
	}
	current, err := os.Lstat(absolute)
	if err != nil {
		return fmt.Errorf("%w: private volume disappeared", ErrStale)
	}
	if !current.IsDir() || !os.SameFile(volume.info, current) || current.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: private volume is not the inspected directory", ErrStale)
	}
	if isReparsePoint(absolute, current) {
		return fmt.Errorf("%w: private volume is a reparse point", ErrUnsafePath)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return fmt.Errorf("remove private volume: %w", err)
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(volume.info, opened) {
		return fmt.Errorf("%w: private volume changed before removal", ErrStale)
	}
	if _, _, err = measurePrivate(ctx, root, limits); err != nil {
		return err
	}
	if err = removeContents(ctx, root, absolute); err != nil {
		return err
	}
	// Windows refuses to remove a directory while its traversal handle is open.
	if err = root.Close(); err != nil {
		return fmt.Errorf("close private volume: %w", err)
	}
	// Revalidate immediately before the final native removal.
	current, err = os.Lstat(absolute)
	if err != nil || !os.SameFile(volume.info, current) {
		return fmt.Errorf("%w: private volume changed during removal", ErrStale)
	}
	if err = os.Remove(absolute); err != nil {
		return fmt.Errorf("remove private volume: %w", err)
	}
	return nil
}

func privatePath(path string, limits PrivateLimits) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: private volume path must be absolute", ErrInvalid)
	}
	if len(path) > maxPathBytes {
		return "", fmt.Errorf("%w: private volume path is too long", ErrInvalid)
	}
	absolute := filepath.Clean(path)
	if broadTarget(absolute) {
		return "", fmt.Errorf("%w: private volume must not be a root, home, temporary, system, or mount root", ErrUnsafePath)
	}
	depth := relativeComponents(absolute)
	if depth > limits.MaxDepth {
		return "", fmt.Errorf("%w: private volume path exceeds the depth bound of %d", ErrLimit, limits.MaxDepth)
	}
	for _, component := range strings.Split(relativePath(absolute), string(filepath.Separator)) {
		if len(component) > maxComponent {
			return "", fmt.Errorf("%w: private volume path component is too long", ErrInvalid)
		}
	}
	return absolute, nil
}

func rejectUnsafeComponents(absolute string, limits PrivateLimits) error {
	volume := filepath.VolumeName(absolute)
	current := volume + string(filepath.Separator)
	rest := strings.TrimPrefix(absolute, current)
	components := strings.Split(rest, string(filepath.Separator))
	if len(components) > limits.MaxDepth {
		return fmt.Errorf("%w: path depth bound exceeded", ErrLimit)
	}
	for _, component := range components {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect private volume component: %w", err)
		}
		alias := systemAlias(current)
		if (info.Mode()&os.ModeSymlink != 0 || isReparsePoint(current, info)) && !alias {
			return fmt.Errorf("%w: private volume path contains a link", ErrUnsafePath)
		}
		if !info.IsDir() && !alias && current != absolute {
			return fmt.Errorf("%w: private volume path component is not a directory", ErrUnsafePath)
		}
	}
	return nil
}

func measurePrivate(ctx context.Context, root *os.Root, limits PrivateLimits) (int, int64, error) {
	entries := 0
	bytes := int64(0)
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		entries++
		if entries > limits.MaxEntries {
			return fmt.Errorf("%w: private volume exceeds the entry bound of %d", ErrLimit, limits.MaxEntries)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(filepath.Join(root.Name(), path), info) {
			return fmt.Errorf("%w: private volume contains a link", ErrUnsafePath)
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%w: private volume contains a special file", ErrUnsafePath)
			}
			bytes += info.Size()
			if bytes > limits.MaxBytes {
				return fmt.Errorf("%w: private volume exceeds the byte bound of %d", ErrLimit, limits.MaxBytes)
			}
		}
		return nil
	})
	return entries, bytes, err
}

func removeContents(ctx context.Context, root *os.Root, absolute string) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := removeWithin(ctx, root, name, filepath.Join(absolute, name)); err != nil {
			return err
		}
	}
	return nil
}

func removeWithin(ctx context.Context, root *os.Root, name, absolute string) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(absolute, info) {
		return fmt.Errorf("%w: private volume contains a link", ErrUnsafePath)
	}
	if info.IsDir() {
		child, err := root.OpenRoot(name)
		if err != nil {
			return err
		}
		defer child.Close()
		entries, err := fs.ReadDir(child.FS(), ".")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := removeWithin(ctx, child, entry.Name(), filepath.Join(absolute, entry.Name())); err != nil {
				return err
			}
		}
		if err := child.Close(); err != nil {
			return err
		}
		current, err := root.Lstat(name)
		if err != nil || !os.SameFile(info, current) {
			return fmt.Errorf("%w: private directory changed during removal", ErrStale)
		}
		return root.Remove(name)
	}
	return root.Remove(name)
}

func privateIdentity(path string, info os.FileInfo) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d", path, info.Size(), info.Mode(), info.ModTime().UnixNano())))
	return hex.EncodeToString(digest[:])
}

func relativeComponents(path string) int {
	relative := relativePath(path)
	if relative == "" {
		return 0
	}
	return len(strings.Split(relative, string(filepath.Separator)))
}

func relativePath(path string) string {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	rest = strings.TrimPrefix(rest, string(filepath.Separator))
	return rest
}

func broadTarget(path string) bool {
	volume := filepath.VolumeName(path)
	if path == volume+string(filepath.Separator) || path == string(filepath.Separator) {
		return true
	}
	home, _ := os.UserHomeDir()
	temp := os.TempDir()
	for _, value := range []string{home, temp} {
		if value != "" && samePath(path, value) {
			return true
		}
	}
	if relativeComponents(path) < 2 {
		return true
	}
	for _, value := range systemDirectories() {
		if samePath(path, value) {
			return true
		}
	}
	return isMountRoot(path)
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func systemDirectories() []string {
	if os.PathSeparator == '\\' {
		return []string{
			`C:\Users`, `C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`,
			`C:\ProgramData`, `C:\System Volume Information`, `C:\PerfLogs`, `C:\Recovery`,
		}
	}
	return []string{
		"/Users", "/home", "/root", "/var", "/tmp", "/private", "/etc", "/usr", "/bin", "/sbin",
		"/opt", "/Library", "/System", "/Applications", "/Volumes", "/dev", "/proc", "/sys",
		"/run", "/mnt", "/media", "/srv", "/boot", "/snap",
	}
}
