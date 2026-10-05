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

func edgeConfig() chromiumengine.Config {
	return chromiumengine.Config{
		Name: "edge", MacUserData: "Microsoft Edge", WindowsUserData: `Microsoft\Edge\User Data`, LinuxUserData: "microsoft-edge",
		KeychainService: "Microsoft Edge Safe Storage", KeychainAccount: "Microsoft Edge", SecretApplication: "microsoft-edge",
		WalletFolder: "Microsoft Edge Keys", WalletKey: "Microsoft Edge Safe Storage",
		Extensions: chromiumengine.ExtensionManagementConfig{
			Executables: browserdiscovery.ExecutableLocations{
				Darwin:  []string{"Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
				Linux:   []string{"microsoft-edge", "microsoft-edge-stable"},
				Windows: []string{"Microsoft/Edge/Application/msedge.exe"},
			},
			ExtensionPage:        "edge://extensions/",
			ManagedPolicyDrivers: map[string]string{"windows": "powershell-registry"},
			Store: &chromiumengine.StoreConfig{
				DefaultStore: "edge", UpdateURLs: map[string]string{
					"edge":   "https://edge.microsoft.com/extensionwebstorebase/v1/crx",
					"chrome": "https://clients2.google.com/service/update2/crx",
				},
				WindowsVendor:      `Microsoft\Edge`,
				MacUserDirectory:   "Library/Application Support/Microsoft Edge/External Extensions",
				MacSystemDirectory: "/Library/Application Support/Microsoft/Edge/External Extensions",
				LinuxDirectory:     ".config/microsoft-edge/External Extensions", LinuxDirectoryInHome: true,
				LinuxAdditionalDirectories: []string{"/usr/share/microsoft-edge/extensions"},
			},
		},
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 4 && args[1] == "management" {
		return chromiumengine.RunManagement(edgeConfig(), args[0], input, stdout, stderr)
	}
	if len(args) != 3 {
		fmt.Fprintln(stderr, "ctx: Edge sharing needs profile resource operation")
		return 2
	}
	profile, resource, operation := args[0], args[1], args[2]
	edge := edgeConfig()
	switch resource {
	case "status":
		if operation == "probe" {
			return kit.Encode(stdout, share.AvailabilityReport{Version: share.AvailabilityVersion, Operations: chromiumengine.Probe(edge, profile)})
		}
	case "cookie":
		return kit.RunCookie(profile, operation, input, stdout, stderr, chromiumengine.NewCookieBackend(edge))
	case "policy":
		if operation == "export" {
			return browserpolicy.RunPolicyExport(input, stdout, stderr, edgePolicies())
		}
	}
	fmt.Fprintln(stderr, "ctx: unsupported Edge share resource or operation")
	return 2
}

func edgePolicies() browserpolicy.PolicySources {
	switch runtime.GOOS {
	case "linux":
		return browserpolicy.PolicySources{Roots: []browserpolicy.PolicyRoot{{Path: "/etc/opt/edge/policies/managed", Level: "managed"}, {Path: "/etc/opt/edge/policies/recommended", Level: "recommended"}}}
	case "darwin":
		return browserpolicy.PolicySources{Files: chromiumengine.ManagedPreferenceFiles("com.microsoft.Edge")}
	case "windows":
		return browserpolicy.PolicySources{RegistryKey: `Software\Policies\Microsoft\Edge`}
	default:
		return browserpolicy.PolicySources{}
	}
}
