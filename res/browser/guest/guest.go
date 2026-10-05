// Package guest provides portable browser guest helpers for serving CTX
// resource requests. Product-specific operations remain in browser adapters.
package guest

import (
	"context"
	"io"

	kit "github.com/webong/ctx/internal/app/browser/adapterkit"
	"github.com/webong/ctx/res/browser/contract"
)

type ManagementBackend = kit.ManagementBackend
type PageSessionBackend = kit.PageSessionBackend
type NativeExtensionBackend = kit.NativeExtensionBackend
type ExtensionInspector = kit.ExtensionInspector
type UserscriptSessionBackend = kit.UserscriptSessionBackend
type UserscriptTargetSessionBackend = kit.UserscriptTargetSessionBackend
type CookieBackend = kit.CookieBackend
type PolicyFile = kit.PolicyFile
type PolicyRoot = kit.PolicyRoot
type PolicySources = kit.PolicySources

func RunCookie(profile, operation string, input io.Reader, stdout, stderr io.Writer, backend CookieBackend) int {
	return kit.RunCookie(profile, operation, input, stdout, stderr, backend)
}

func RunPolicyExport(input io.Reader, stdout, stderr io.Writer, sources PolicySources) int {
	return kit.RunPolicyExport(input, stdout, stderr, sources)
}

func RunManagement(ctx context.Context, profile string, input io.Reader, stdout, stderr io.Writer, backend ManagementBackend) int {
	return kit.RunManagement(ctx, profile, input, stdout, stderr, backend)
}

func RunLocalManagement(ctx context.Context, browserName, profile string, input io.Reader, stdout, stderr io.Writer, backend NativeExtensionBackend) int {
	return kit.RunLocalManagement(ctx, browserName, profile, input, stdout, stderr, backend)
}

func Encode(output io.Writer, value any) int      { return kit.Encode(output, value) }
func ReportError(stderr io.Writer, err error) int { return kit.ReportError(stderr, err) }
func ReportErrorCode(stderr io.Writer, err error, code int) int {
	return kit.ReportErrorCode(stderr, err, code)
}

func ExportPolicies(sources PolicySources) (contract.PolicyBundle, error) {
	return kit.ExportPolicies(sources)
}

func ManagedPreferenceFiles(domain string) []PolicyFile { return kit.ManagedPreferenceFiles(domain) }

func CookieHostSQL(host string) string { return kit.CookieHostSQL(host) }
func CookieDatabaseColumns(database, table string) ([]string, error) {
	return kit.CookieDatabaseColumns(database, table)
}
func ReadableCookieDatabase(database, table string) (string, func(), []string, error) {
	return kit.ReadableCookieDatabase(database, table)
}
func SnapshotCookieDatabase(database string) (string, func(), error) {
	return kit.SnapshotCookieDatabase(database)
}
func CopyPrivateFile(source, target string) error { return kit.CopyPrivateFile(source, target) }
func HasSQLiteColumn(columns []string, wanted string) bool {
	return kit.HasSQLiteColumn(columns, wanted)
}
func RunSQLite(database string, readonly bool, query string) ([]byte, error) {
	return kit.RunSQLite(database, readonly, query)
}
func SQLString(value string) string     { return kit.SQLString(value) }
func SQLIdentifier(value string) string { return kit.SQLIdentifier(value) }
func SQLBool(value bool) string         { return kit.SQLBool(value) }
