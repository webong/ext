package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/webong/ext/res/web/host/contract"
)

// Safari owns its managed-preference locations and the plutil invocation.
func runSafariPolicyExport(input io.Reader, stdout, stderr io.Writer) int {
	if _, err := contract.DecodePolicyRequest(input); err != nil {
		fmt.Fprintln(stderr, "safari adapter: invalid policy request")
		return 2
	}
	bundle := contract.PolicyBundle{Version: contract.Version, Entries: []contract.PolicyEntry{}}
	for _, path := range []string{
		filepath.Join("/Library/Managed Preferences", "com.apple.Safari.plist"),
		filepath.Join("/Library/Managed Preferences", os.Getenv("USER"), "com.apple.Safari.plist"),
	} {
		entry, err := readSafariPolicyFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "safari adapter: %v\n", err)
			return 1
		}
		if entry != nil {
			bundle.Entries = append(bundle.Entries, *entry)
		}
	}
	if err := bundle.Validate(); err != nil {
		fmt.Fprintf(stderr, "safari adapter: invalid policy bundle: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(bundle); err != nil {
		return 1
	}
	return 0
}

func readSafariPolicyFile(path string) (*contract.PolicyEntry, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return nil, fmt.Errorf("policy file %s is not a regular file under 2 MiB", path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	content, err := exec.CommandContext(ctx, "plutil", "-convert", "xml1", "-o", "-", path).Output()
	cancel()
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, fmt.Errorf("policy file %s contains binary data", path)
	}
	return &contract.PolicyEntry{Location: path, Level: "managed", Format: "plist", Content: strings.TrimSpace(string(content))}, nil
}
