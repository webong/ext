package main

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/webong/ctx/adapters/browserdiscovery"
	"github.com/webong/ctx/adapters/browserpolicy"
	chromiumengine "github.com/webong/ctx/adapters/chromium/engine"
	share "github.com/webong/ctx/res/browser/contract"
	kit "github.com/webong/ctx/res/browser/guest"
)

func chromeConfig() chromiumengine.Config {
	return chromiumengine.Config{
		Name: "chrome", MacUserData: "Google/Chrome", WindowsUserData: `Google\Chrome\User Data`, LinuxUserData: "google-chrome",
		KeychainService: "Chrome Safe Storage", KeychainAccount: "Chrome", SecretApplication: "chrome",
		WalletFolder: "Chrome Keys", WalletKey: "Chrome Safe Storage",
		Extensions: chromiumengine.ExtensionManagementConfig{
			Executables: browserdiscovery.ExecutableLocations{
				Darwin:  []string{"Google Chrome.app/Contents/MacOS/Google Chrome", "Chrome.app/Contents/MacOS/Google Chrome"},
				Linux:   []string{"google-chrome", "google-chrome-stable"},
				Windows: []string{"Google/Chrome/Application/chrome.exe"},
			},
			ExtensionPage: "chrome://extensions/", DebuggingRequiresCustomProfile: true,
			LinuxConfigHomeEnv:   "CHROME_CONFIG_HOME",
			LinuxPolicyPath:      "/etc/opt/chrome/policies/managed/ctx-extensions.json",
			ManagedPolicyDrivers: map[string]string{"windows": "powershell-registry", "linux": "managed-json"},
			Store: &chromiumengine.StoreConfig{
				DefaultStore: "chrome", UpdateURLs: map[string]string{"chrome": "https://clients2.google.com/service/update2/crx"},
				WindowsVendor:              `Google\Chrome`,
				MacUserDirectory:           "Library/Application Support/Google/Chrome/External Extensions",
				MacSystemDirectory:         "/Library/Application Support/Google/Chrome/External Extensions",
				LinuxDirectory:             "/opt/google/chrome/extensions",
				LinuxAdditionalDirectories: []string{"/usr/share/google-chrome/extensions"},
				LinuxLocalCRX:              true,
				LinuxUpdateURL:             true,
			},
		},
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 4 && args[1] == "management" {
		return chromiumengine.RunManagement(chromeConfig(), args[0], input, stdout, stderr)
	}
	if len(args) != 3 {
		fmt.Fprintln(stderr, "ctx: Chrome sharing needs profile resource operation")
		return 2
	}
	profile, resource, operation := args[0], args[1], args[2]
	chrome := chromeConfig()
	switch resource {
	case "status":
		if operation == "probe" {
			return kit.Encode(stdout, share.AvailabilityReport{Version: share.AvailabilityVersion, Operations: chromiumengine.Probe(chrome, profile)})
		}
	case "cookie":
		return kit.RunCookie(profile, operation, input, stdout, stderr, chromiumengine.NewCookieBackend(chrome))
	case "policy":
		if operation == "export" {
			return browserpolicy.RunPolicyExport(input, stdout, stderr, chromePolicies())
		}
	}
	fmt.Fprintln(stderr, "ctx: unsupported Chrome share resource or operation")
	return 2
}

func chromePolicies() browserpolicy.PolicySources {
	switch runtime.GOOS {
	case "linux":
		return browserpolicy.PolicySources{Roots: []browserpolicy.PolicyRoot{{Path: "/etc/opt/chrome/policies/managed", Level: "managed"}, {Path: "/etc/opt/chrome/policies/recommended", Level: "recommended"}}}
	case "darwin":
		return browserpolicy.PolicySources{Files: chromiumengine.ManagedPreferenceFiles("com.google.Chrome")}
	case "windows":
		return browserpolicy.PolicySources{RegistryKey: `Software\Policies\Google\Chrome`}
	default:
		return browserpolicy.PolicySources{}
	}
}
