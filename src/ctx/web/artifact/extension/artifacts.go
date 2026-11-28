package extension

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BuildResult identifies an artifact produced by an adapter with caller-owned
// tools and signing credentials. It never implies installation.
type BuildResult struct {
	Status           string `json:"status"`
	Browser          string `json:"browser"`
	Source           string `json:"source"`
	SourceRevision   string `json:"sourceRevision"`
	ArtifactRevision string `json:"artifactRevision"`
	KeyPath          string `json:"keyPath,omitempty"`
	ID               string `json:"id,omitempty"`
	Version          string `json:"version,omitempty"`
	NextAction       string `json:"nextAction,omitempty"`
}

// CheckedSource inspects source and requires it to match the reviewed revision.
func CheckedSource(source, revision string) (Description, error) {
	if revision == "" {
		return Description{}, errors.New("a prepared source revision is required")
	}
	description, err := Inspect(source)
	if err != nil {
		return Description{}, err
	}
	if description.Revision != revision {
		return Description{}, errors.New("extension changed since inspection")
	}
	return description, nil
}

// UnusedArtifact checks the output suffix and creates its parent directory.
// The caller must still create the output exclusively to avoid overwriting it.
func UnusedArtifact(path, extension string) error {
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), extension) {
		return fmt.Errorf("output must be an absolute %s path", extension)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("output already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(filepath.Dir(path), 0755)
}

// OutsideSource prevents an artifact from being written into its source tree.
func OutsideSource(source, output string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	root, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if target == root || strings.HasPrefix(target, root+string(filepath.Separator)) {
		return errors.New("artifact and private key paths must be outside the extension source")
	}
	return nil
}

// CopyArtifactExclusive copies a file without replacing an existing output.
func CopyArtifactExclusive(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		return errors.Join(copyErr, closeErr)
	}
	return nil
}

// FileSHA256 hashes a native artifact without interpreting its format.
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
