// Package volume provides bounded snapshot and restore mechanics for a
// caller-selected local volume or directory. Identity, authorization,
// retention, and transport policy belong to the caller.
package volume

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Limits bounds decompressed archive contents during restore.
type Limits struct {
	Bytes   int64
	Entries int
}

// Export writes a gzip-compressed tar archive containing regular files and
// directories below root. It refuses symlinks and special files so a snapshot
// cannot unexpectedly escape onto another host surface.
func Export(ctx context.Context, output io.Writer, root string) error {
	pinned, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open volume root: %w", err)
	}
	defer pinned.Close()
	return ExportRoot(ctx, output, pinned)
}

// ExportRoot exports from a pinned directory handle so concurrent renames
// cannot redirect traversal outside the selected root.
func ExportRoot(ctx context.Context, output io.Writer, root *os.Root) error {
	if output == nil || root == nil {
		return errors.New("volume export requires an output and root")
	}
	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	walkErr := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("snapshot refuses unsupported file %q", path)
		}
		var input *os.File
		if !info.IsDir() {
			input, err = root.Open(path)
			if err != nil {
				return err
			}
			info, err = input.Stat()
			if err != nil {
				input.Close()
				return err
			}
			if !info.Mode().IsRegular() {
				input.Close()
				return errors.New("snapshot source changed type")
			}
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(path)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			if input != nil {
				input.Close()
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		_, copyErr := io.Copy(tarWriter, contextReader{ctx: ctx, reader: input})
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeTarErr := tarWriter.Close()
	closeGzipErr := gzipWriter.Close()
	if walkErr != nil {
		return walkErr
	}
	if closeTarErr != nil {
		return closeTarErr
	}
	return closeGzipErr
}

// Restore restores an archive into an empty destination with conservative
// default extraction limits.
func Restore(ctx context.Context, archivePath, destination string) error {
	return RestoreWithLimits(ctx, archivePath, destination, Limits{Bytes: 1 << 30, Entries: 100_000})
}

// RestoreWithLimits stages extraction inside the empty destination and moves
// the validated top-level entries into place only after extraction succeeds.
func RestoreWithLimits(ctx context.Context, archivePath, destination string, limits Limits) error {
	if err := ensureEmptyDirectory(destination); err != nil {
		return err
	}
	pinned, err := os.OpenRoot(destination)
	if err != nil {
		return fmt.Errorf("open volume destination: %w", err)
	}
	defer pinned.Close()
	return RestoreRootWithLimits(ctx, archivePath, pinned, limits)
}

// RestoreRootWithLimits restores into a pinned empty directory.
func RestoreRootWithLimits(ctx context.Context, archivePath string, destination *os.Root, limits Limits) error {
	if destination == nil {
		return errors.New("volume restore requires a destination")
	}
	if limits.Bytes <= 0 || limits.Entries <= 0 {
		return errors.New("archive requires positive extraction limits")
	}
	existing, err := fs.ReadDir(destination.FS(), ".")
	if err != nil {
		return err
	}
	if len(existing) != 0 {
		return errors.New("volume restore requires an empty destination")
	}
	var key [16]byte
	if _, err = rand.Read(key[:]); err != nil {
		return err
	}
	temporary := ".restore-" + hex.EncodeToString(key[:])
	if err = destination.Mkdir(temporary, 0o700); err != nil {
		return err
	}
	defer destination.RemoveAll(temporary)
	stage, err := destination.OpenRoot(temporary)
	if err != nil {
		return err
	}
	defer stage.Close()
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("open volume snapshot: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	var extracted int64
	entriesRead := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read volume snapshot: %w", err)
		}
		entriesRead++
		if header.Size < 0 || header.Size > limits.Bytes-extracted || entriesRead > limits.Entries {
			return errors.New("archive extraction limit exceeded")
		}
		extracted += header.Size
		path, err := archiveDestination(header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := stage.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := stage.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			output, err := stage.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, contextReader{ctx: ctx, reader: tarReader})
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("snapshot contains unsupported archive entry %q", header.Name)
		}
	}
	entries, err := fs.ReadDir(stage.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := destination.Rename(filepath.Join(temporary, entry.Name()), entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// FileDigest returns an archive's byte size and SHA-256 digest.
func FileDigest(path string) (int64, string, error) {
	input, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer input.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, input)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func ensureEmptyDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("volume restore requires an empty destination")
	}
	return nil
}

func archiveDestination(name string) (string, error) {
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("snapshot archive entry %q uses a non-portable separator", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || filepath.VolumeName(clean) != "" ||
		filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("snapshot archive entry %q escapes destination", name)
	}
	return clean, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
