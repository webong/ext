package bundlecmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type nativeReceipt struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Dir     string `json:"dir"`
	Current string `json:"current"`
	Pending string `json:"pending,omitempty"`
}

func (n preparedNative) statePaths() (string, string, error) {
	dir := filepath.Join(n.Root, ".ext-bundle-state")
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("native state is not a real directory")
		}
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	hash := sha256.Sum256([]byte(n.Relative))
	base := filepath.Join(dir, hex.EncodeToString(hash[:]))
	return base + ".json", base + ".backup", nil
}
func (n preparedNative) directoryDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("native output is not a real directory: %s", path)
	}
	var digest string
	err = n.lib.Library().Call("directory_digest", map[string]any{"root": path}, &digest)
	return digest, err
}
func validNativeDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
func (n preparedNative) readReceipt(path string) (nativeReceipt, error) {
	receipt := nativeReceipt{Version: 1, ID: n.ID, Dir: n.Relative}
	data, err := read(path, 4096)
	if os.IsNotExist(err) {
		return receipt, nil
	}
	if err != nil {
		return receipt, err
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receipt, err
	}
	if receipt.Version != 1 || receipt.ID != n.ID || receipt.Dir != n.Relative ||
		(receipt.Current != "" && !validNativeDigest(receipt.Current)) ||
		(receipt.Pending != "" && !validNativeDigest(receipt.Pending)) {
		return receipt, fmt.Errorf("native receipt does not match destination")
	}
	return receipt, nil
}
func saveNativeReceipt(path string, receipt nativeReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (n preparedNative) inspectState() (nativeReceipt, string, string, string, string, error) {
	dest, err := nativeDestination(n.Root, n.Relative)
	if err != nil {
		return nativeReceipt{}, "", "", "", "", err
	}
	receiptPath, backup, err := n.statePaths()
	if err != nil {
		return nativeReceipt{}, "", "", "", "", err
	}
	r, err := n.readReceipt(receiptPath)
	if err != nil {
		return r, "", "", "", "", err
	}
	have, err := n.directoryDigest(dest)
	if err != nil {
		return r, "", "", "", "", err
	}
	old, err := n.directoryDigest(backup)
	if err != nil {
		return r, "", "", "", "", err
	}
	if r.Pending != "" && r.Pending != n.Digest {
		return r, "", "", "", "", fmt.Errorf("native upgrade pending; rerun the original bundle before another replacement")
	}
	if old != "" && (r.Pending == "" || old != r.Current || r.Current == "") {
		return r, "", "", "", "", fmt.Errorf("native backup differs from receipt")
	}
	if have != "" && have != n.Digest && (r.Current == "" || have != r.Current) {
		return r, "", "", "", "", fmt.Errorf("native destination %s contains different content than its receipt; preserve user changes before replacing it", dest)
	}
	if old != "" && have != "" && have != n.Digest {
		return r, "", "", "", "", fmt.Errorf("native upgrade has conflicting old outputs")
	}
	if r.Pending != "" && r.Current != "" && have == "" && old == "" {
		return r, "", "", "", "", fmt.Errorf("pending native upgrade lost its previous output")
	}
	return r, receiptPath, backup, dest, have, nil
}
func (n preparedNative) currentMatches() (bool, error) {
	_, _, _, _, have, err := n.inspectState()
	return have == n.Digest, err
}

func (n preparedNative) publish() error {
	r, path, backup, dest, have, err := n.inspectState()
	if err != nil {
		return err
	}
	if have != n.Digest {
		// Persist intent before moving any published bytes. Replaying this pending
		// record checks both the previous output and the newly prepared output.
		if r.Pending == "" {
			r.Current = have
			r.Pending = n.Digest
			if err := saveNativeReceipt(path, r); err != nil {
				return err
			}
		}
		if have != "" {
			if err := os.Rename(dest, backup); err != nil {
				return err
			}
			if n.afterBackup != nil {
				if err := n.afterBackup(); err != nil {
					return err
				}
			}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if _, err := nativeDestination(n.Root, n.Relative); err != nil {
			return err
		}
		if err := os.Rename(n.Stage, dest); err != nil {
			return err
		}
		if n.afterPublish != nil {
			if err := n.afterPublish(); err != nil {
				return err
			}
		}
	}
	actual, err := n.directoryDigest(dest)
	if err != nil {
		return err
	}
	if actual != n.Digest {
		return fmt.Errorf("published native output failed digest verification")
	}
	old, err := n.directoryDigest(backup)
	if err != nil {
		return err
	}
	if old != "" {
		if old != r.Current {
			return fmt.Errorf("native backup changed during publication")
		}
		if err := os.RemoveAll(backup); err != nil {
			return err
		}
	}
	r.Current = n.Digest
	r.Pending = ""
	return saveNativeReceipt(path, r)
}
