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

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// This file is the same in engine/plugin/host/store and cgoengine/plugin/host/store: it uses
// only what both packages declare.

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
	path := archive.Source
	switch {
	case strings.HasPrefix(path, "https://"):
		path = filepath.Join(work, "archive")
		if err := Download(ctx, options.Client, archive.Source, path, s.limits.MaxArchiveBytes); err != nil {
			return Installed{}, err
		}
	case strings.Contains(path, "://"):
		return Installed{}, fmt.Errorf("%w: a remote archive requires an HTTPS URL", plugin.ErrInvalid)
	}
	root, err := ExtractArchive(path, sum, filepath.Join(work, "package"), s.limits)
	if err != nil {
		return Installed{}, err
	}
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	identity, err := archiveIdentity(root)
	if err != nil {
		return Installed{}, err
	}
	if archive.ID != "" && identity.ID != archive.ID {
		return Installed{}, fmt.Errorf("%w: archive holds another package", plugin.ErrMismatch)
	}
	installed, err := s.Load(identity.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		installed, err = s.InstallWithOptions(root, options.Copy)
	case err != nil:
	case installed.Manifest.Descriptor.Identity.Revision == identity.Revision:
		var have, want string
		if have, err = installed.Digest(); err == nil {
			if want, err = plugin.DirectoryDigest(root); err == nil && have != want {
				err = fmt.Errorf("%w: archive differs from the installed package of the same revision", plugin.ErrMismatch)
			}
		}
	default:
		installed, err = s.UpgradeWithOptions(root, options.Copy)
	}
	if err != nil {
		return Installed{}, err
	}
	receipt := Receipt{ID: identity.ID, Revision: installed.Manifest.Descriptor.Identity.Revision, SHA256: sum}
	if err := s.writeReceipt(receipt); err != nil {
		return Installed{}, err
	}
	return installed, nil
}

// Receipt returns the install receipt of a package, or ErrNotFound when no
// archive install recorded one. A receipt describes the install that wrote it:
// compare its Revision with the installed package before relying on it.
func (s *Store) Receipt(id string) (Receipt, error) {
	if !validID(id) {
		return Receipt{}, plugin.ErrInvalid
	}
	return s.readReceipt(dirName(id) + ".json")
}

func (s *Store) receiptPath(id string) string {
	return filepath.Join(s.root, receiptsName, dirName(id)+".json")
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

func (s *Store) writeReceipt(receipt Receipt) error {
	directory := filepath.Join(s.root, receiptsName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".receipt-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	_, writeErr := temporary.Write(append(data, '\n'))
	if closeErr := temporary.Close(); writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr)
	}
	return os.Rename(name, s.receiptPath(receipt.ID))
}

// dropReceipt forgets the receipt of a removed package.
func (s *Store) dropReceipt(id string) { _ = os.Remove(s.receiptPath(id)) }

// archiveIdentity reads the identity an extracted package claims. The install
// that follows validates the manifest in full.
func archiveIdentity(root string) (plugin.Identity, error) {
	file, err := os.Open(filepath.Join(root, ManifestName))
	if err != nil {
		return plugin.Identity{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16<<20+1))
	if err != nil {
		return plugin.Identity{}, err
	}
	if len(data) > 16<<20 {
		return plugin.Identity{}, fmt.Errorf("%w: manifest exceeds 16 MiB", plugin.ErrInvalid)
	}
	var manifest packagekit.Manifest
	if err := plugin.Decode(data, &manifest); err != nil {
		return plugin.Identity{}, err
	}
	identity := manifest.Descriptor.Identity
	if err := identity.Validate(); err != nil {
		return plugin.Identity{}, err
	}
	return identity, nil
}
