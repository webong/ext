package chromium

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"

	"github.com/webong/ctx/adapters/chromium/engine/webextension"
	share "github.com/webong/ctx/res/browser/contract"
	kit "github.com/webong/ctx/res/browser/guest"
)

// Run serves the versioned cookie protocol for a Chromium-family adapter.
// Each adapter supplies its own storage paths and credential identity.
func Run(config Config, args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 4 && args[1] == "management" {
		return RunManagement(config, args[0], input, stdout, stderr)
	}
	if len(args) != 3 {
		fmt.Fprintln(stderr, "ctx: browser sharing needs profile resource operation")
		return 2
	}
	profile, resource, operation := args[0], args[1], args[2]
	switch resource {
	case "status":
		if operation == "probe" {
			operations := Probe(config, profile)
			delete(operations, "policy.export")
			return kit.Encode(stdout, share.AvailabilityReport{Version: share.AvailabilityVersion, Operations: operations})
		}
	case "cookie":
		return kit.RunCookie(profile, operation, input, stdout, stderr, NewCookieBackend(config))
	}
	fmt.Fprintln(stderr, "ctx: unsupported browser share resource or operation")
	return 2
}

// NewCookieBackend keeps credential lookup and derived keys within one cookie
// operation. Construct a new backend for each request; it is not concurrency safe.
func NewCookieBackend(config Config) kit.CookieBackend {
	decryptor := newChromiumCookieDecryptor(config)
	return kit.CookieBackend{
		Normalize: func(profile, storeID string, payload json.RawMessage) (share.CookieQueryResult, error) {
			return webextension.Normalize(webextension.Policy{Browser: config.Name, Namespace: "chromium", Partition: true}, profile, storeID, payload)
		},
		QueryHandleIsStorePath: true,
		Query: func(profile string, site *url.URL, includeExpired bool) ([]share.Cookie, string, error) {
			return Query(config, profile, site, includeExpired)
		},
		List: func(profile string, site *url.URL, name string) ([]share.Cookie, string, error) {
			return List(config, profile, site, name)
		},
		ReadValue: func(database string, cookie share.Cookie) (string, error) {
			return readChromiumCookieValueWithDecryptor(database, cookie, decryptor.decrypt)
		},
		Import: func(profile string, cookie share.Cookie, replace bool) error {
			return Import(config, profile, cookie, replace)
		},
	}
}
