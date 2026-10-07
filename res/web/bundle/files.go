package bundle

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// confinedRoot opens files below one directory and nowhere else. A path that
// leaves the directory by ".." or by a symbolic link is refused, as is anything
// whose name starts with a dot (.env, .git).
type confinedRoot struct{ root string }

func newConfinedRoot(directory string) (confinedRoot, error) {
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return confinedRoot{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return confinedRoot{}, err
	}
	if !info.IsDir() {
		return confinedRoot{}, errors.New(directory + " is not a directory")
	}
	return confinedRoot{root: resolved}, nil
}

var errOutside = errors.New("path is outside the bundle")

// open returns a regular file for the slash-separated request path.
func (c confinedRoot) open(name string) (*os.File, fs.FileInfo, error) {
	// ".." is refused outright rather than collapsed, so a request that tries to
	// climb is an error and never quietly maps to some other file.
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return nil, nil, errOutside
		}
	}
	clean := path.Clean("/" + name)[1:]
	if clean == "" {
		return nil, nil, fs.ErrNotExist
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.HasPrefix(part, ".") {
			return nil, nil, errOutside
		}
	}
	real, err := filepath.EvalSymlinks(filepath.Join(c.root, filepath.FromSlash(clean)))
	if err != nil {
		return nil, nil, err
	}
	relative, err := filepath.Rel(c.root, real)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, nil, errOutside
	}
	file, err := os.Open(real)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, fs.ErrNotExist
	}
	return file, info, nil
}
