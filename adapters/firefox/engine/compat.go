package firefox

import (
	"io"

	share "github.com/webong/ctx/res/browser/contract"
	kit "github.com/webong/ctx/res/browser/guest"
	"github.com/webong/ctx/res/browser/sqlite"
)

type browserCookie = share.Cookie
type browserResourceRequest = share.ResourceRequest
type browserResourceBundle = share.ResourceBundle

var (
	cookieDatabaseColumns  = sqlite.CookieDatabaseColumns
	readableCookieDatabase = sqlite.ReadableCookieDatabase
	snapshotCookieDatabase = sqlite.SnapshotCookieDatabase
	copyPrivateFile        = sqlite.CopyPrivateFile
	hasSQLiteColumn        = sqlite.HasSQLiteColumn
	runSQLite              = sqlite.RunSQLite
	sqlString              = sqlite.SQLString
	sqlIdentifier          = sqlite.SQLIdentifier
	sqlBool                = sqlite.SQLBool
	cookieHostSQL          = sqlite.CookieHostSQL
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
