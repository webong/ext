package main

import (
	"encoding/json"
	"fmt"
	"github.com/webong/ext/pkg/plugin"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"github.com/webong/ext/adapters/chromium/engine/webextension"
	browsershare "github.com/webong/ext/res/web/contract"
	kit "github.com/webong/ext/res/web/guest"
	"github.com/webong/ext/res/web/policy"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, input io.Reader, stdout, stderr io.Writer) int {
	request, err := plugin.ParseAdapterInvocation(args)
	if err != nil || request.Operation != "share" || runtime.GOOS != "darwin" {
		fmt.Fprintln(stderr, "ctx: Safari sharing requires macOS")
		return 2
	}
	profile, words := request.Selection, request.Arguments
	if len(words) == 3 && words[0] == "management" {
		return kit.RunLocalManagement(nil, "safari", profile, input, stdout, stderr, safariExtensionBackend{})
	}
	if len(words) != 2 {
		fmt.Fprintln(stderr, "ctx: Safari sharing needs profile resource operation")
		return 2
	}
	resource, operation := words[0], words[1]
	switch resource {
	case "status":
		if operation == "probe" {
			return kit.Encode(stdout, browsershare.AvailabilityReport{Version: browsershare.AvailabilityVersion, Operations: safariShareStatus(profile)})
		}
	case "cookie":
		if operation == "list" || operation == "export" || operation == "query" || operation == "normalize" {
			return kit.RunCookie(profile, operation, input, stdout, stderr, kit.CookieBackend{
				Normalize: func(profile, storeID string, payload json.RawMessage) (browsershare.CookieQueryResult, error) {
					return webextension.Normalize(webextension.Policy{Browser: "safari", Namespace: "safari"}, profile, storeID, payload)
				},
				QueryHandleIsStorePath: true,
				List:                   readSafariSiteCookies, ReadValue: readSafariCookieValue, QueryValues: true,
				Query: func(profile string, site *url.URL, includeExpired bool) ([]browsershare.Cookie, string, error) {
					return querySafariCookies(profile, site, "", includeExpired, true)
				},
			})
		}
	case "policy":
		if operation == "export" {
			return policy.RunPolicyExport(input, stdout, stderr, policy.PolicySources{Files: []policy.PolicyFile{
				{Path: filepath.Join("/Library/Managed Preferences", "com.apple.Safari.plist"), Level: "managed", Format: "plist"},
				{Path: filepath.Join("/Library/Managed Preferences", os.Getenv("USER"), "com.apple.Safari.plist"), Level: "managed", Format: "plist"},
			}})
		}
	}
	fmt.Fprintln(stderr, "ctx: unsupported Safari share resource or operation")
	return 2
}
