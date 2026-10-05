package main

import (
	"fmt"
	"io"
	"os"
	"runtime"

	chromiumengine "github.com/webong/ctx/adapters/chromium/engine"
	share "github.com/webong/ctx/res/browser/contract"
	kit "github.com/webong/ctx/res/browser/guest"
)

func braveConfig() chromiumengine.Config {
	return chromiumengine.Config{
		Name: "brave", MacUserData: "BraveSoftware/Brave-Browser", WindowsUserData: `BraveSoftware\Brave-Browser\User Data`, LinuxUserData: "BraveSoftware/Brave-Browser",
		KeychainService: "Brave Safe Storage", KeychainAccount: "Brave", SecretApplication: "brave",
		WalletFolder: "Brave Keys", WalletKey: "Brave Safe Storage",
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "ctx: Brave sharing needs profile resource operation")
		return 2
	}
	profile, resource, operation := args[0], args[1], args[2]
	brave := braveConfig()
	switch resource {
	case "status":
		if operation == "probe" {
			return kit.Encode(stdout, share.AvailabilityReport{Version: share.AvailabilityVersion, Operations: chromiumengine.Probe(brave, profile)})
		}
	case "cookie":
		return kit.RunCookie(profile, operation, input, stdout, stderr, chromiumengine.NewCookieBackend(brave))
	case "policy":
		if operation == "export" {
			return kit.RunPolicyExport(input, stdout, stderr, bravePolicies())
		}
	}
	fmt.Fprintln(stderr, "ctx: unsupported Brave share resource or operation")
	return 2
}

func bravePolicies() kit.PolicySources {
	switch runtime.GOOS {
	case "linux":
		return kit.PolicySources{Roots: []kit.PolicyRoot{{Path: "/etc/brave/policies/managed", Level: "managed"}, {Path: "/etc/brave/policies/recommended", Level: "recommended"}}}
	case "darwin":
		return kit.PolicySources{Files: kit.ManagedPreferenceFiles("com.brave.Browser")}
	case "windows":
		return kit.PolicySources{RegistryKey: `Software\Policies\BraveSoftware\Brave`}
	default:
		return kit.PolicySources{}
	}
}
