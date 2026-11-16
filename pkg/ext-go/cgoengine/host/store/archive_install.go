package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cgoengine"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// The install from an archive runs in the engine's package store, as every other operation of a
// Store does: the digest check, the extraction, the install or upgrade and the receipt. What stays
// here is what the engine leaves to its caller: fetching an HTTPS source, and looking for a receipt
// first so that a repeated install downloads nothing.

// receiptsName is the directory under the store root that holds install
// receipts. Its leading "." keeps it out of every listing.
const receiptsName = ".receipts"

// Archive names one pinned package archive.
type Archive struct {
	// Source is a local file path or an HTTPS URL.
	Source string
	// SHA256 is the required hexadecimal digest of the archive file.
	SHA256 string
	// ID, when set, is the package the archive must hold.
	ID string
}

// ArchiveOptions adjusts InstallArchive. The zero value is ready to use.
type ArchiveOptions struct {
	// Client downloads an HTTPS source. Nil uses NewDownloadClient.
	Client HTTPClient
	// Copy is the copy policy of the install or upgrade.
	Copy CopyOptions
}

// Receipt records which pinned archive produced an installed package.
type Receipt struct {
	// ID is the package.
	ID string `json:"id"`
	// Revision is the package revision the install produced.
	Revision string `json:"revision"`
	// SHA256 is the digest of the archive file, in lower case.
	SHA256 string `json:"artifact_sha256"`
}

// InstallArchive installs the package in a pinned archive, or upgrades the
// installed package of the same ID to it. The archive is a local file or an
// HTTPS URL, in any format ExtractArchive reads; it is checked against
// archive.SHA256 before anything is extracted, extracted under the store's
// Limits as ExtractArchive describes, and then installed like a directory, so
// the package is verified against its manifest. An upgrade follows the rules of
// Upgrade. A package already installed at the archive's revision must have the
// archive's exact content.
//
// A successful install leaves a Receipt. When the receipt of an installed
// package names this archive's digest and the package's current revision, and
// the package still verifies, the call returns it without reading or
// downloading the archive.
func (s *Store) InstallArchive(ctx context.Context, archive Archive, options ArchiveOptions) (Installed, error) {
	sum, err := normalizeSHA256(archive.SHA256)
	if err != nil {
		return Installed{}, err
	}
	if archive.Source == "" || archive.ID != "" && !validID(archive.ID) {
		return Installed{}, plugin.ErrInvalid
	}
	s.archives.Lock()
	defer s.archives.Unlock()
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	path := archive.Source
	switch {
	case strings.HasPrefix(path, "https://"):
		// The engine looks for a receipt before it opens a file; look before fetching one.
		if installed, ok := s.receipted(sum, archive.ID); ok {
			return installed, nil
		}
		if err := os.MkdirAll(s.root, 0o700); err != nil {
			return Installed{}, err
		}
		work, err := os.MkdirTemp(s.root, ".archive-")
		if err != nil {
			return Installed{}, err
		}
		defer os.RemoveAll(work)
		path = filepath.Join(work, "archive")
		if err := Download(ctx, options.Client, archive.Source, path, s.limits.MaxArchiveBytes); err != nil {
			return Installed{}, err
		}
		if err := ctx.Err(); err != nil {
			return Installed{}, err
		}
	case strings.Contains(path, "://"):
		return Installed{}, fmt.Errorf("%w: a remote archive requires an HTTPS URL", plugin.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine(func(e *cgoengine.PackageStore) (Installed, error) {
		manifest, err := e.InstallArchive(path, sum, archive.ID, options.Copy.PreserveExecutableArtifacts)
		if err != nil {
			return Installed{}, storeError(err)
		}
		return s.installed(manifest)
	})
}

// Receipt returns the install receipt of a package, or ErrNotFound when no
// archive install recorded one. A receipt describes the install that wrote it:
// compare its Revision with the installed package before relying on it.
func (s *Store) Receipt(id string) (Receipt, error) {
	if !validID(id) {
		return Receipt{}, plugin.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.open()
	if err != nil {
		return Receipt{}, err
	}
	defer e.Close()
	data, err := e.Receipt(id)
	if err != nil {
		return Receipt{}, storeError(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.ID != id {
		return Receipt{}, fmt.Errorf("%w: install receipt is not valid", plugin.ErrInvalid)
	}
	return receipt, nil
}

func (s *Store) readReceipt(name string) (Receipt, error) {
	file, err := os.Open(filepath.Join(s.root, receiptsName, name))
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, ErrNotFound
	}
	if err != nil {
		return Receipt{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<16))
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil || !validID(receipt.ID) || dirName(receipt.ID)+".json" != name {
		return Receipt{}, fmt.Errorf("%w: install receipt is not valid", plugin.ErrInvalid)
	}
	return receipt, nil
}

// receipted finds the installed package a receipt ties to the archive digest:
// same digest, same revision as installed, and still verifying.
func (s *Store) receipted(sum, id string) (Installed, bool) {
	entries, err := os.ReadDir(filepath.Join(s.root, receiptsName))
	if err != nil {
		return Installed{}, false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		receipt, err := s.readReceipt(entry.Name())
		if err != nil || receipt.SHA256 != sum || id != "" && receipt.ID != id {
			continue
		}
		installed, err := s.Load(receipt.ID)
		if err == nil && installed.Manifest.Descriptor.Identity.Revision == receipt.Revision {
			return installed, true
		}
	}
	return Installed{}, false
}
