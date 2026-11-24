package pkgcmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
)

// RequireMigration prevents creating a fresh store over an earlier installation.
// The published packages directory is authoritative; old roots are backups only.
func RequireMigration(home string) error {
	target := filepath.Join(home, "packages")
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("package root is not a directory: %s", target)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, name := range []string{"commands", "adapters"} {
		entries, err := os.ReadDir(filepath.Join(home, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if len(entries) > 0 {
			return fmt.Errorf("legacy package stores found; run ext package migrate before managing packages")
		}
	}
	return nil
}

func RunMigration(home string, stdout, stderr io.Writer) int {
	if err := migrate(home); err != nil {
		fmt.Fprintf(stderr, "ext: package migrate: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "package store ready; legacy stores are retained as backups and are no longer read")
	return 0
}

// migrate stages verified packages and their original trust scopes together,
// then publishes with one rename. Failure leaves the old installation intact.
func migrate(home string) error {
	target := filepath.Join(home, "packages")
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("package root is not a directory: %s", target)
		}
		return nil // Never reimport grants after revocation or removal.
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	lock := filepath.Join(home, ".package-migration.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("migration already running or stale lock at %s: %w", lock, err)
	}
	defer os.Remove(lock)
	stage, err := os.MkdirTemp(home, ".packages-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	dest, err := store.New(stage, store.Limits{})
	if err != nil {
		return err
	}
	for _, name := range []string{"commands", "adapters"} {
		root := filepath.Join(home, name)
		if info, err := os.Lstat(root); err == nil && !info.IsDir() {
			return fmt.Errorf("legacy root is not a directory: %s", root)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		source, err := store.New(root, store.Limits{})
		if err != nil {
			return err
		}
		packages, checks, err := source.ListUnverified()
		if err != nil {
			return err
		}
		for _, check := range checks {
			if !check.OK() {
				if name == "adapters" && (check.ID == "trust" || check.ID == "package-trust.json") {
					continue
				}
				return fmt.Errorf("%s: %w", name, check.Err)
			}
		}
		for _, p := range packages {
			digest, err := p.Digest()
			if err != nil {
				return fmt.Errorf("%s/%s: %w", name, p.Name(), err)
			}
			previous, err := dest.Load(p.Name())
			if err == nil {
				previousDigest, err := previous.Digest()
				if err != nil {
					return err
				}
				if previousDigest != digest {
					return fmt.Errorf("package identity collision for %s; legacy stores contain different content", p.Name())
				}
				continue
			}
			if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if _, err := dest.InstallWithOptions(p.Directory, store.CopyOptions{PreserveExecutableArtifacts: true}); err != nil {
				return err
			}
		}
	}
	// Import the complete old host ledger, retaining all consumer scopes.
	ledger, err := hosttrust.New(filepath.Join(stage, sharedTrustFile))
	if err != nil {
		return err
	}
	if err := importLedger(filepath.Join(home, "package-trust.json"), ledger); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(home, "package-trust.json")); errors.Is(err, os.ErrNotExist) {
		data, err := boundedFile(filepath.Join(home, TrustFile))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			var old struct {
				Packages map[string]string `json:"packages"`
			}
			if err := plugin.Decode(data, &old); err != nil {
				return err
			}
			if err := ledger.ImportLegacy(scope, old.Packages); err != nil {
				return err
			}
		}
	}
	oldAdapters := filepath.Join(home, "adapters")
	adapterLedger := filepath.Join(oldAdapters, "package-trust.json")
	if err := importLedger(adapterLedger, ledger); err != nil {
		return err
	}
	if _, err := os.Stat(adapterLedger); errors.Is(err, os.ErrNotExist) {
		data, err := boundedFile(filepath.Join(oldAdapters, "trust"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			records := map[string]string{}
			scan := bufio.NewScanner(strings.NewReader(string(data)))
			for scan.Scan() {
				fields := strings.Fields(scan.Text())
				if len(fields) != 2 {
					return fmt.Errorf("invalid legacy adapter trust record")
				}
				records[fields[0]] = fields[1]
			}
			if err := scan.Err(); err != nil {
				return err
			}
			if err := ledger.ImportLegacy("ctx.adapter/v2", records); err != nil {
				return err
			}
		}
	}
	// Keep archive receipts, without interpreting or changing their bytes.
	for _, name := range []string{"commands", "adapters"} {
		root := filepath.Join(home, name, ".receipts")
		if info, err := os.Lstat(root); err == nil && !info.IsDir() {
			return fmt.Errorf("receipt root is not a directory: %s", root)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			data, err := boundedFile(filepath.Join(root, entry.Name()))
			if err != nil {
				return err
			}
			dir := filepath.Join(stage, ".receipts")
			if err := os.MkdirAll(dir, 0700); err != nil {
				return err
			}
			path := filepath.Join(dir, entry.Name())
			if old, err := os.ReadFile(path); err == nil && string(old) != string(data) {
				return fmt.Errorf("receipt collision: %s", entry.Name())
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				return err
			}
		}
	}
	return os.Rename(stage, target)
}

func boundedFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, fmt.Errorf("invalid migration file: %s", path)
	}
	return os.ReadFile(path)
}

func importLedger(path string, dest *hosttrust.File) error {
	data, err := boundedFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc struct {
		APIVersion string             `json:"apiVersion"`
		Records    []hosttrust.Record `json:"records"`
	}
	if err := plugin.Decode(data, &doc); err != nil {
		return err
	}
	if doc.APIVersion != "ext.trust/v1" || doc.Records == nil {
		return fmt.Errorf("invalid trust ledger: %s", path)
	}
	source, err := hosttrust.New(path)
	if err != nil {
		return err
	}
	// Records validates the whole ledger, including duplicate keys and bounds.
	if _, err := source.Records(scope); err != nil {
		return err
	}
	for _, r := range doc.Records {
		existing, err := dest.Records(r.Scope)
		if err != nil {
			return err
		}
		for _, old := range existing {
			if old.ID == r.ID && old.Digest != r.Digest {
				return fmt.Errorf("trust collision for %s in %s", r.ID, r.Scope)
			}
		}
		if err := dest.Record(r.Scope, r.ID, r.Digest); err != nil {
			return err
		}
	}
	return nil
}
