package chromium

import (
	"os"
	"path/filepath"

	"github.com/webong/ext/res/web/policy"
)

// ManagedPreferenceFiles supplies the macOS policy locations for a
// Chromium-family adapter's declared preference domain.
func ManagedPreferenceFiles(domain string) []policy.PolicyFile {
	return []policy.PolicyFile{
		{Path: filepath.Join("/Library/Managed Preferences", domain+".plist"), Level: "managed", Format: "plist"},
		{Path: filepath.Join("/Library/Managed Preferences", os.Getenv("USER"), domain+".plist"), Level: "managed", Format: "plist"},
	}
}
