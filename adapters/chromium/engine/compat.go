package chromium

import (
	"net/url"
	"os/exec"
	"runtime"

	"github.com/webong/ctx/adapters/sqlite"
	share "github.com/webong/ctx/res/browser/contract"
)

// Config contains browser identity and storage conventions supplied by its adapter.
type Config struct {
	Name            string
	MacUserData     string
	WindowsUserData string
	WindowsRoaming  bool
	LinuxUserData   string
	KeychainService string
	KeychainAccount string
	// KeychainPath optionally selects an isolated macOS keychain. Empty uses
	// the user's configured search list; no search-list changes are made.
	KeychainPath      string
	SecretApplication string
	WalletFolder      string
	WalletKey         string
	Extensions        ExtensionManagementConfig
}

// Cookie is the portable cookie type returned by the Chromium engine. It is
// re-exported so adapters outside this module can use the engine without
// importing CTX's internal browser packages.
type Cookie = share.Cookie

type browserCookie = Cookie

var (
	cookieDatabaseColumns  = sqlite.CookieDatabaseColumns
	readableCookieDatabase = sqlite.ReadableCookieDatabase
	hasSQLiteColumn        = sqlite.HasSQLiteColumn
	runSQLite              = sqlite.RunSQLite
	sqlString              = sqlite.SQLString
	sqlIdentifier          = sqlite.SQLIdentifier
	sqlBool                = sqlite.SQLBool
	cookieHostSQL          = sqlite.CookieHostSQL
)

func cookieActive(cookie browserCookie) bool { return share.CookieActive(cookie) }
func cookieDomainMatches(siteHost, cookieDomain string) bool {
	return share.CookieDomainMatches(siteHost, cookieDomain)
}

func List(config Config, profile string, site *url.URL, name string) ([]Cookie, string, error) {
	return readChromiumSiteCookies(config, profile, site, name)
}
func Query(config Config, profile string, site *url.URL, includeExpired bool) ([]Cookie, string, error) {
	return queryChromiumCookies(config, profile, site, "", includeExpired)
}
func ReadValue(config Config, database string, cookie Cookie) (string, error) {
	return readChromiumCookieValue(config, database, cookie)
}
func Import(config Config, profile string, cookie Cookie, replace bool) error {
	return importChromiumCookie(config, profile, cookie, replace)
}

// Probe checks local prerequisites without opening a cookie value or querying
// an OS credential helper. Export and import remain unknown until attempted.
func Probe(config Config, profile string) map[string]string {
	result := map[string]string{
		"cookie.normalize": "ready",
		"cookie.list":      "blocked", "cookie.export": "blocked", "cookie.query": "blocked", "cookie.import": "blocked",
		"policy.export": "ready",
	}
	if _, err := chromiumCookieDatabase(config, profile); err != nil {
		return result
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return result
	}
	result["cookie.list"] = "ready"
	result["cookie.export"] = "unknown"
	result["cookie.query"] = "unknown"
	if runtime.GOOS != "windows" {
		result["cookie.import"] = "unknown"
	}
	return result
}
