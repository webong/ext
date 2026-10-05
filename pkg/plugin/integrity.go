package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DirectoryDigest hashes every regular file, including manifests and assets,
// in sorted slash-path order. Symlinks and special files are rejected. The
// digest format preserves CTX's existing adapter trust records:
// SHA256(concat("./" + relativePath + " " + fileSHA256 + "\n")).
// Installers must protect the directory against concurrent replacement; this
// function is integrity checking, not an OS sandbox or atomic exec primitive.
func DirectoryDigest(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: package root must be a directory", ErrInvalid)
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%w: package entries must be regular files", ErrInvalid)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.ContainsAny(relative, "\r\n") {
			return fmt.Errorf("%w: package paths cannot contain line breaks", ErrInvalid)
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	outer := sha256.New()
	for _, path := range files {
		file, err := os.Open(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return "", err
		}
		inner := sha256.New()
		_, copyErr := io.Copy(inner, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		fmt.Fprintf(outer, "./%s %s\n", path, hex.EncodeToString(inner.Sum(nil)))
	}
	return hex.EncodeToString(outer.Sum(nil)), nil
}
