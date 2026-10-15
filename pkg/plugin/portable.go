package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var devicePattern = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)`)

// PortablePath reports whether value is a portable relative path: slash
// separated, with no empty, "." or ".." component, no backslash, colon, NUL or
// line break, no component ending in "." or a space, no Windows device name,
// and at most 1024 bytes. Package artifact paths must satisfy it.
func PortablePath(value string) bool {
	if !fs.ValidPath(value) || value == "." || path.Clean(value) != value || strings.ContainsAny(value, "\\:\x00\r\n") || len(value) > 1024 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || devicePattern.MatchString(part) {
			return false
		}
	}
	return true
}

// TreeDigest returns DirectoryDigest of the directory dir (a slash-separated
// portable path) under root, for a tree artifact. Every component of dir must be
// a plain directory, and the directory must hold at least one file and only
// regular files and directories.
func TreeDigest(root, dir string) (string, error) {
	if !PortablePath(dir) {
		return "", fmt.Errorf("%w: tree path %q is not portable", ErrInvalid, dir)
	}
	target := filepath.Join(root, filepath.FromSlash(dir))
	current := root
	for _, part := range strings.Split(dir, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%w: %s is not a plain directory", ErrInvalid, dir)
		}
	}
	empty := true
	err := filepath.WalkDir(target, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			empty = false
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if empty {
		return "", fmt.Errorf("%w: tree %s holds no files", ErrInvalid, dir)
	}
	return DirectoryDigest(target)
}
