package chromium

import (
	"os"
	"path/filepath"

	"github.com/webong/ctx/adapters/browserpolicy"
)

// ManagedPreferenceFiles supplies the macOS policy locations for a
// Chromium-family adapter's declared preference domain.
func ManagedPreferenceFiles(domain string) []browserpolicy.PolicyFile {
	return []browserpolicy.PolicyFile{
		{Path: filepath.Join("/Library/Managed Preferences", domain+".plist"), Level: "managed", Format: "plist"},
		{Path: filepath.Join("/Library/Managed Preferences", os.Getenv("USER"), domain+".plist"), Level: "managed", Format: "plist"},
	}
}
