package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// A Copy is a built C shared library a program carries in its binary, as the package ext lib fetch
// writes for the purego driver embeds it: its file name, its bytes and their SHA-256.
type Copy struct {
	File   string
	Data   []byte
	SHA256 string
}

var provided sync.Map // library -> Copy

// ErrNotProvided is a library no package of this program provides a copy of.
var ErrNotProvided = errors.New("no copy of this library is provided")

// Provide records the copy of library (its C prefix, such as ext_plugin) this program carries. The
// package ext lib fetch writes calls it from init; a second, different copy of the same library is
// a build mistake and panics.
func Provide(library string, c Copy) {
	if previous, loaded := provided.LoadOrStore(library, c); loaded && previous.(Copy).SHA256 != c.SHA256 {
		panic(fmt.Sprintf("library: two different copies of %s are provided", library))
	}
}

// Provided returns the copy of library this program carries.
func Provided(library string) (Copy, bool) {
	c, ok := provided.Load(library)
	if !ok {
		return Copy{}, false
	}
	return c.(Copy), true
}

// OpenProvided opens the copy of library this program carries. The copy is written once under dir
// (the user cache's ext-go/library when dir is empty), in a directory named for its digest, after its
// bytes are checked against that digest; an existing file there is used only if it matches.
func OpenProvided(library, dir string) (*Library, error) {
	c, ok := Provided(library)
	if !ok {
		return nil, fmt.Errorf("%s: %w", library, ErrNotProvided)
	}
	path, err := Materialize(c, library, dir)
	if err != nil {
		return nil, err
	}
	return Open(path)
}

// Materialize writes c under dir/<library>/<sha256>/<file> and returns the path, reusing a file that
// is already there with the same contents.
func Materialize(c Copy, library, dir string) (string, error) {
	sum := sha256.Sum256(c.Data)
	if hex.EncodeToString(sum[:]) != c.SHA256 {
		return "", fmt.Errorf("library: the provided %s does not match its sha256", library)
	}
	if c.File == "" || filepath.Base(c.File) != c.File {
		return "", fmt.Errorf("library: provided file name %q", c.File)
	}
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cache, "ext-go", "library")
	}
	target := filepath.Join(dir, library, c.SHA256, c.File)
	if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, c.Data) {
		return target, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".copy-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(c.Data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return target, nil
}
