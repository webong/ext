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

func chromiumConfig() chromiumengine.Config {
	return chromiumengine.Config{
		Name: "chromium", MacUserData: "Chromium", WindowsUserData: `Chromium\User Data`, LinuxUserData: "chromium",
		KeychainService: "Chromium Safe Storage", KeychainAccount: "Chromium", SecretApplication: "chromium",
		WalletFolder: "Chromium Keys", WalletKey: "Chromium Safe Storage",
		Extensions: chromiumengine.ExtensionManagementConfig{
			Executables: browserdiscovery.ExecutableLocations{
				Darwin:  []string{"Chromium.app/Contents/MacOS/Chromium"},
				Linux:   []string{"chromium", "chromium-browser"},
				Windows: []string{"Chromium/Application/chrome.exe"},
			},
			ExtensionPage: "chrome://extensions/",
		},
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 4 && args[1] == "management" {
		return chromiumengine.RunManagement(chromiumConfig(), args[0], input, stdout, stderr)
	}
	if len(args) != 3 {
		fmt.Fprintln(stderr, "ctx: Chromium sharing needs profile resource operation")
		return 2
	}
	profile, resource, operation := args[0], args[1], args[2]
	chromium := chromiumConfig()
	switch resource {
	case "status":
		if operation == "probe" {
			return kit.Encode(stdout, share.AvailabilityReport{Version: share.AvailabilityVersion, Operations: chromiumengine.Probe(chromium, profile)})
		}
	case "cookie":
		return kit.RunCookie(profile, operation, input, stdout, stderr, chromiumengine.NewCookieBackend(chromium))
	case "policy":
		if operation == "export" {
			return browserpolicy.RunPolicyExport(input, stdout, stderr, chromiumPolicies())
		}
	}
	fmt.Fprintln(stderr, "ctx: unsupported Chromium share resource or operation")
	return 2
}

func chromiumPolicies() browserpolicy.PolicySources {
	switch runtime.GOOS {
	case "linux":
		return browserpolicy.PolicySources{Roots: []browserpolicy.PolicyRoot{
			{Path: "/etc/chromium/policies/managed", Level: "managed"}, {Path: "/etc/chromium/policies/recommended", Level: "recommended"},
			{Path: "/etc/chromium-browser/policies/managed", Level: "managed"}, {Path: "/etc/chromium-browser/policies/recommended", Level: "recommended"},
		}}
	case "darwin":
		return browserpolicy.PolicySources{Files: chromiumengine.ManagedPreferenceFiles("org.chromium.Chromium")}
	case "windows":
		return browserpolicy.PolicySources{RegistryKey: `Software\Policies\Chromium`}
	default:
		return browserpolicy.PolicySources{}
	}
}
