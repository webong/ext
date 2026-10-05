package firefox

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/webong/ctx/adapters/chromium/engine/webextension"
	browsershare "github.com/webong/ctx/res/browser/contract"
	"github.com/webong/ctx/res/browser/extension"
	kit "github.com/webong/ctx/res/browser/guest"
)

// Config identifies the browser's profile registry. Cookie and NSS handling
// remains shared among Firefox-family adapters.
type Config struct {
	Name                     string
	MacProfileRoot           string
	WindowsProfileRoot       string
	LinuxProfileRoot         string
	LinuxFallbackProfileRoot string
	ExtensionExecutables     extension.ExecutableLocations
	NativeExtensions         bool
}

func Run(config Config, args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 4 && args[1] == "management" {
		return kit.RunLocalManagement(nil, config.Name, args[0], input, stdout, stderr, extensionBackend{config: config})
	}
	if len(args) != 3 {
		fmt.Fprintln(stderr, "ctx: Firefox sharing needs profile resource operation")
		return 2
	}
	profile, resource, operation := args[0], args[1], args[2]
	switch resource {
	case "status":
		if operation == "probe" {
			return kit.Encode(stdout, firefoxShareStatus(config, profile))
		}
	case "cookie":
		return kit.RunCookie(profile, operation, input, stdout, stderr, kit.CookieBackend{
			Normalize: func(profile, storeID string, payload json.RawMessage) (browsershare.CookieQueryResult, error) {
				return webextension.Normalize(webextension.Policy{Browser: config.Name, Namespace: "firefox", Partition: true, FirstPartyDomain: true}, profile, storeID, payload)
			},
			QueryHandleIsStorePath: true,
			Query: func(profile string, site *url.URL, includeExpired bool) ([]browsershare.Cookie, string, error) {
				return queryFirefoxCookies(config, profile, site, "", includeExpired)
			},
			List: func(profile string, site *url.URL, name string) ([]browsershare.Cookie, string, error) {
				return readFirefoxSiteCookies(config, profile, site, name)
			},
			ReadValue: readFirefoxCookieValue,
			Import: func(profile string, cookie browsershare.Cookie, replace bool) error {
				return importFirefoxCookie(config, profile, cookie, replace)
			},
		})
	case "certificate":
		return nativeFirefoxCertificateCommand(config, profile, operation, input, stdout, stderr)
	case "policy":
		if operation != "export" {
			break
		}
		if config.Name == "firefox" {
			return kit.RunPolicyExport(input, stdout, stderr, firefoxPolicySources())
		}
	}
	fmt.Fprintln(stderr, "ctx: unsupported Firefox share resource or operation")
	return 2
}

func firefoxShareStatus(config Config, profile string) browsershare.AvailabilityReport {
	operations := map[string]string{"cookie.normalize": "ready"}
	if config.Name == "firefox" {
		operations["policy.export"] = "ready"
	}
	storeReady := false
	if _, err := firefoxCookieDatabase(config, profile); err == nil {
		_, err = exec.LookPath("sqlite3")
		storeReady = err == nil
	}
	for _, name := range []string{"cookie.list", "cookie.export", "cookie.query", "cookie.import"} {
		operations[name] = "blocked"
		if storeReady {
			operations[name] = "ready"
			if name == "cookie.import" {
				operations[name] = "unknown" // target profile state is checked at import time
			}
		}
	}
	certificates := "blocked"
	if _, err := exec.LookPath("certutil"); err == nil {
		if _, err := firefoxCookieDatabase(config, profile); err == nil {
			certificates = "unknown"
		}
	}
	operations["certificate.list"] = certificates
	operations["certificate.export"] = certificates
	operations["certificate.import"] = certificates
	if _, err := exec.LookPath("pk12util"); err != nil {
		operations["certificate.export"] = "blocked"
		operations["certificate.import"] = "blocked"
	}
	return browsershare.AvailabilityReport{Version: browsershare.AvailabilityVersion, Operations: operations}
}

func firefoxPolicySources() kit.PolicySources {
	switch runtime.GOOS {
	case "linux":
		return kit.PolicySources{Files: []kit.PolicyFile{
			{Path: "/etc/firefox/policies/policies.json", Level: "managed", Format: "json"},
			{Path: "/usr/lib/firefox/distribution/policies.json", Level: "managed", Format: "json"},
			{Path: "/usr/lib64/firefox/distribution/policies.json", Level: "managed", Format: "json"},
		}}
	case "darwin":
		sources := kit.PolicySources{Files: kit.ManagedPreferenceFiles("org.mozilla.firefox")}
		for _, path := range []string{"/Applications/Firefox.app/Contents/Resources/distribution/policies.json", filepath.Join(os.Getenv("HOME"), "Applications/Firefox.app/Contents/Resources/distribution/policies.json")} {
			sources.Files = append(sources.Files, kit.PolicyFile{Path: path, Level: "managed", Format: "json"})
		}
		return sources
	case "windows":
		return kit.PolicySources{RegistryKey: `Software\Policies\Mozilla\Firefox`}
	default:
		return kit.PolicySources{}
	}
}
