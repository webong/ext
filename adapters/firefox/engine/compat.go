package firefox

import (
	"io"

	share "github.com/webong/ctx/res/browser/contract"
	kit "github.com/webong/ctx/res/browser/guest"
)

type browserCookie = share.Cookie
type browserResourceRequest = share.ResourceRequest
type browserResourceBundle = share.ResourceBundle

var (
	cookieDatabaseColumns  = kit.CookieDatabaseColumns
	readableCookieDatabase = kit.ReadableCookieDatabase
	snapshotCookieDatabase = kit.SnapshotCookieDatabase
	copyPrivateFile        = kit.CopyPrivateFile
	hasSQLiteColumn        = kit.HasSQLiteColumn
	runSQLite              = kit.RunSQLite
	sqlString              = kit.SQLString
	sqlIdentifier          = kit.SQLIdentifier
	sqlBool                = kit.SQLBool
	cookieHostSQL          = kit.CookieHostSQL
)

func cookieActive(cookie browserCookie) bool { return share.CookieActive(cookie) }
func validateBrowserResourceBundle(bundle browserResourceBundle, resource string) error {
	return share.ValidateResourceBundle(bundle, resource)
}
func encodeBrowserNative(output io.Writer, value any) int { return kit.Encode(output, value) }
func reportError(stderr io.Writer, err error) int         { return kit.ReportError(stderr, err) }
func reportErrorCode(stderr io.Writer, err error, code int) int {
	return kit.ReportErrorCode(stderr, err, code)
}
