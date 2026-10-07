package main

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/webong/ext/pkg/plugin"

	chromiumengine "github.com/webong/ext/adapters/chromium/engine"
	share "github.com/webong/ext/res/web/contract"
	"github.com/webong/ext/res/web/discovery"
	kit "github.com/webong/ext/res/web/guest"
	"github.com/webong/ext/res/web/policy"
)

func chromeConfig() chromiumengine.Config {
	return chromiumengine.Config{
		Name: "chrome", MacUserData: "Google/Chrome", WindowsUserData: `Google\Chrome\User Data`, LinuxUserData: "google-chrome",
		KeychainService: "Chrome Safe Storage", KeychainAccount: "Chrome", SecretApplication: "chrome",
		WalletFolder: "Chrome Keys", WalletKey: "Chrome Safe Storage",
		Extensions: chromiumengine.ExtensionManagementConfig{
			Executables: discovery.ExecutableLocations{
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
	request, err := plugin.ParseAdapterInvocation(args)
	if err != nil || request.Operation != "share" {
		fmt.Fprintln(stderr, "ctx: Chrome sharing needs profile resource operation")
		return 2
	}
	profile, words := request.Selection, request.Arguments
	if len(words) == 3 && words[0] == "management" {
		return chromiumengine.RunManagement(chromeConfig(), profile, input, stdout, stderr)
	}
	if len(words) != 2 {
		fmt.Fprintln(stderr, "ctx: Chrome sharing needs profile resource operation")
		return 2
	}
	resource, operation := words[0], words[1]
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
			return policy.RunPolicyExport(input, stdout, stderr, chromePolicies())
		}
	}
	fmt.Fprintln(stderr, "ctx: unsupported Chrome share resource or operation")
	return 2
}

func chromePolicies() policy.PolicySources {
	switch runtime.GOOS {
	case "linux":
		return policy.PolicySources{Roots: []policy.PolicyRoot{{Path: "/etc/opt/chrome/policies/managed", Level: "managed"}, {Path: "/etc/opt/chrome/policies/recommended", Level: "recommended"}}}
	case "darwin":
		return policy.PolicySources{Files: chromiumengine.ManagedPreferenceFiles("com.google.Chrome")}
	case "windows":
		return policy.PolicySources{RegistryKey: `Software\Policies\Google\Chrome`}
	default:
		return policy.PolicySources{}
	}
}
