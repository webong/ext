package main

import (
	"github.com/webong/ctx/adapters/sqlite"
	share "github.com/webong/ctx/res/browser/contract"
)

// These helpers keep the native package's existing fixture access while the
// Firefox-family implementation lives in the Firefox adapter engine.
var (
	copyPrivateFile = sqlite.CopyPrivateFile
	hasSQLiteColumn = sqlite.HasSQLiteColumn
	runSQLite       = sqlite.RunSQLite
)

func cookieDomainMatches(siteHost, cookieDomain string) bool {
	return share.CookieDomainMatches(siteHost, cookieDomain)
}

func readableFirefoxCookieDatabase(database string) (string, func(), []string, error) {
	return sqlite.ReadableCookieDatabase(database, "moz_cookies")
}
