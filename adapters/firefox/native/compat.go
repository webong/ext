package main

import (
	share "github.com/webong/ctx/res/browser/contract"
	kit "github.com/webong/ctx/res/browser/guest"
)

// These helpers keep the native package's existing fixture access while the
// Firefox-family implementation lives in the Firefox adapter engine.
var (
	copyPrivateFile = kit.CopyPrivateFile
	hasSQLiteColumn = kit.HasSQLiteColumn
	runSQLite       = kit.RunSQLite
)

func cookieDomainMatches(siteHost, cookieDomain string) bool {
	return share.CookieDomainMatches(siteHost, cookieDomain)
}

func readableFirefoxCookieDatabase(database string) (string, func(), []string, error) {
	return kit.ReadableCookieDatabase(database, "moz_cookies")
}
