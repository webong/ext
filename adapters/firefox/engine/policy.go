package firefox

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
	"runtime"
	"strings"
	"time"

	"github.com/webong/ext/res/web/host/contract"
)

// Firefox owns its managed-policy locations and native reads. The wire format
// is the portable browser policy contract, not a Chromium Go API.
func runFirefoxPolicyExport(input io.Reader, stdout, stderr io.Writer) int {
	if _, err := contract.DecodePolicyRequest(input); err != nil {
		fmt.Fprintln(stderr, "firefox adapter: invalid policy request")
		return 2
	}
	bundle, err := exportFirefoxPolicies()
	if err != nil {
		fmt.Fprintf(stderr, "firefox adapter: %v\n", err)
		return 1
	}
	if err := bundle.Validate(); err != nil {
		fmt.Fprintf(stderr, "firefox adapter: invalid policy bundle: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(bundle); err != nil {
		return 1
	}
	return 0
}

func exportFirefoxPolicies() (contract.PolicyBundle, error) {
	bundle := contract.PolicyBundle{Version: contract.Version, Entries: []contract.PolicyEntry{}}
	var files []struct{ path, format string }
	switch runtime.GOOS {
	case "linux":
		files = []struct{ path, format string }{
			{"/etc/firefox/policies/policies.json", "json"},
			{"/usr/lib/firefox/distribution/policies.json", "json"},
			{"/usr/lib64/firefox/distribution/policies.json", "json"},
		}
	case "darwin":
		files = []struct{ path, format string }{
			{filepath.Join("/Library/Managed Preferences", "org.mozilla.firefox.plist"), "plist"},
			{filepath.Join("/Library/Managed Preferences", os.Getenv("USER"), "org.mozilla.firefox.plist"), "plist"},
			{"/Applications/Firefox.app/Contents/Resources/distribution/policies.json", "json"},
			{filepath.Join(os.Getenv("HOME"), "Applications/Firefox.app/Contents/Resources/distribution/policies.json"), "json"},
		}
	case "windows":
		for _, hive := range []string{"HKLM", "HKCU"} {
			location := hive + `\Software\Policies\Mozilla\Firefox`
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			output, err := exec.CommandContext(ctx, "reg", "query", location, "/s").Output()
			cancel()
			if err == nil && len(output) > 0 {
				bundle.Entries = append(bundle.Entries, contract.PolicyEntry{Location: location, Level: "managed", Format: "reg-query", Content: string(output)})
			}
		}
	}
	for _, file := range files {
		entry, err := readFirefoxPolicyFile(file.path, file.format)
		if err != nil {
			return bundle, err
		}
		if entry != nil {
			bundle.Entries = append(bundle.Entries, *entry)
		}
	}
	return bundle, nil
}

func readFirefoxPolicyFile(path, format string) (*contract.PolicyEntry, error) {
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
	var content []byte
	if format == "plist" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		content, err = exec.CommandContext(ctx, "plutil", "-convert", "xml1", "-o", "-", path).Output()
		cancel()
	} else {
		content, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if format == "json" && !json.Valid(content) {
		return nil, fmt.Errorf("policy file %s is invalid JSON", path)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, fmt.Errorf("policy file %s contains binary data", path)
	}
	return &contract.PolicyEntry{Location: path, Level: "managed", Format: format, Content: strings.TrimSpace(string(content))}, nil
}
