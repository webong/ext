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

func edgeConfig() chromiumengine.Config {
	return chromiumengine.Config{
		Name: "edge", MacUserData: "Microsoft Edge", WindowsUserData: `Microsoft\Edge\User Data`, LinuxUserData: "microsoft-edge",
		KeychainService: "Microsoft Edge Safe Storage", KeychainAccount: "Microsoft Edge", SecretApplication: "microsoft-edge",
		WalletFolder: "Microsoft Edge Keys", WalletKey: "Microsoft Edge Safe Storage",
		Extensions: chromiumengine.ExtensionManagementConfig{
			Executables: discovery.ExecutableLocations{
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
	request, err := plugin.ParseAdapterInvocation(args)
	if err != nil || request.Operation != "share" {
		fmt.Fprintln(stderr, "ctx: Edge sharing needs profile resource operation")
		return 2
	}
	profile, words := request.Selection, request.Arguments
	if len(words) == 3 && words[0] == "management" {
		return chromiumengine.RunManagement(edgeConfig(), profile, input, stdout, stderr)
	}
	if len(words) != 2 {
		fmt.Fprintln(stderr, "ctx: Edge sharing needs profile resource operation")
		return 2
	}
	resource, operation := words[0], words[1]
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
			return policy.RunPolicyExport(input, stdout, stderr, edgePolicies())
		}
	}
	fmt.Fprintln(stderr, "ctx: unsupported Edge share resource or operation")
	return 2
}

func edgePolicies() policy.PolicySources {
	switch runtime.GOOS {
	case "linux":
		return policy.PolicySources{Roots: []policy.PolicyRoot{{Path: "/etc/opt/edge/policies/managed", Level: "managed"}, {Path: "/etc/opt/edge/policies/recommended", Level: "recommended"}}}
	case "darwin":
		return policy.PolicySources{Files: chromiumengine.ManagedPreferenceFiles("com.microsoft.Edge")}
	case "windows":
		return policy.PolicySources{RegistryKey: `Software\Policies\Microsoft\Edge`}
	default:
		return policy.PolicySources{}
	}
}
