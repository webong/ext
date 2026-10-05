package guest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"unicode/utf8"

	"github.com/webong/ctx/res/browser/contract"
)

// CookieBackend is supplied by one browser adapter. The command never chooses
// a browser implementation or receives a provider name.
type CookieBackend struct {
	List  func(profile string, site *url.URL, name string) ([]contract.Cookie, string, error)
	Query func(profile string, site *url.URL, includeExpired bool) ([]contract.Cookie, string, error)
	// QueryHandleIsStorePath lets the adapter declare that its otherwise opaque
	// Query handle is the original on-disk store path suitable for provenance.
	QueryHandleIsStorePath bool
	// QueryValues means Query already populated each cookie's value.
	QueryValues bool
	ReadValue   func(handle string, cookie contract.Cookie) (string, error)
	Import      func(profile string, cookie contract.Cookie, replace bool) error
	Normalize   func(profile, storeID string, payload json.RawMessage) (contract.CookieQueryResult, error)
}

func RunCookie(profile, operation string, input io.Reader, stdout, stderr io.Writer, backend CookieBackend) int {
	var request contract.CookieRequest
	data, err := io.ReadAll(io.LimitReader(input, (8<<20)+1))
	if err != nil || len(data) > 8<<20 || !utf8.Valid(data) || json.Unmarshal(data, &request) != nil || request.Version != contract.Version {
		fmt.Fprintln(stderr, "ctx: invalid browser share request")
		return 2
	}
	switch operation {
	case "normalize":
		if backend.Normalize == nil || request.StoreID == "" || len(request.NativeExport) == 0 {
			fmt.Fprintln(stderr, "ctx: cookie normalization needs an adapter backend, store_id, and native_export")
			return 2
		}
		result, err := backend.Normalize(profile, request.StoreID, request.NativeExport)
		if err != nil {
			return ReportErrorCode(stderr, err, 2)
		}
		if result.Cookies == nil || result.StoreID != request.StoreID {
			fmt.Fprintln(stderr, "ctx: adapter returned an invalid normalized export")
			return 1
		}
		for _, cookie := range result.Cookies {
			if err := contract.ValidateCookie(cookie); err != nil {
				return ReportError(stderr, err)
			}
		}
		return Encode(stdout, result)
	case "query":
		if backend.Query == nil || (!backend.QueryValues && backend.ReadValue == nil) {
			fmt.Fprintln(stderr, "ctx: cookie query is unavailable")
			return 2
		}
		var site *url.URL
		if request.Site != "" {
			var err error
			site, err = contract.ParseSite(request.Site)
			if err != nil {
				return ReportErrorCode(stderr, err, 2)
			}
		} else if !request.AllowAllHosts {
			fmt.Fprintln(stderr, "ctx: cookie query needs a site or allow_all_hosts")
			return 2
		}
		cookies, handle, err := backend.Query(profile, site, request.IncludeExpired)
		if err != nil {
			return ReportError(stderr, err)
		}
		result := contract.CookieQueryResult{Cookies: make([]contract.Cookie, 0, len(cookies))}
		if backend.QueryHandleIsStorePath && filepath.IsAbs(handle) {
			result.StorePath = handle
		}
		for _, cookie := range cookies {
			if err := contract.ValidateCookie(cookie); err != nil {
				result.Warnings = append(result.Warnings, "invalid cookie metadata: "+err.Error())
				continue
			}
			if !contract.CookieMatchesSiteOptions(site, cookie, request.IncludeExpired) {
				continue
			}
			if len(request.Names) > 0 {
				matched := false
				for _, name := range request.Names {
					if cookie.Name == name {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			if !backend.QueryValues {
				value, err := backend.ReadValue(handle, cookie)
				if err != nil {
					result.Warnings = append(result.Warnings, fmt.Sprintf("cookie %q at %q could not be read: %v", cookie.Name, cookie.Domain, err))
					continue
				}
				cookie.Value = value
			}
			if err := contract.ValidateCookie(cookie); err != nil {
				result.Warnings = append(result.Warnings, "invalid cookie value: "+err.Error())
				continue
			}
			result.Cookies = append(result.Cookies, cookie)
		}
		return Encode(stdout, result)
	case "list", "export":
		if backend.List == nil || (operation == "export" && backend.ReadValue == nil) {
			fmt.Fprintln(stderr, "ctx: cookie list or export is unavailable")
			return 2
		}
		site, err := contract.ParseSite(request.Site)
		if err != nil {
			return ReportErrorCode(stderr, err, 2)
		}
		name := ""
		if operation == "export" {
			name = request.Cookie.Name
			if name == "" || (request.Cookie.ID <= 0 && request.Cookie.Ref == "") {
				fmt.Fprintln(stderr, "ctx: cookie export needs a listed cookie ID or reference and name")
				return 2
			}
		}
		cookies, handle, err := backend.List(profile, site, name)
		if err != nil {
			return ReportError(stderr, err)
		}
		if operation == "list" {
			listed := make([]contract.Cookie, 0, len(cookies))
			for _, cookie := range cookies {
				cookie.Value = ""
				if contract.ValidateCookie(cookie) == nil && contract.CookieMatchesSite(site, cookie) {
					listed = append(listed, cookie)
				}
			}
			return Encode(stdout, listed)
		}
		for _, cookie := range cookies {
			if contract.SameListedCookie(cookie, request.Cookie) && contract.CookieMatchesSite(site, cookie) {
				value, err := backend.ReadValue(handle, cookie)
				if err != nil {
					return ReportError(stderr, err)
				}
				cookie.Value = value
				if err := contract.ValidateCookie(cookie); err != nil {
					return ReportError(stderr, err)
				}
				return Encode(stdout, cookie)
			}
		}
		fmt.Fprintln(stderr, "ctx: cookie changed since listing; retry")
		return 1
	case "import":
		if backend.Import == nil {
			fmt.Fprintln(stderr, "ctx: cookie import is unavailable")
			return 2
		}
		if request.Bundle == nil {
			fmt.Fprintln(stderr, "ctx: cookie import needs a bundle")
			return 2
		}
		if err := contract.ValidateCookieBundle(*request.Bundle); err != nil {
			return ReportErrorCode(stderr, err, 2)
		}
		if err := backend.Import(profile, request.Bundle.Cookie, request.Replace); err != nil {
			return ReportError(stderr, err)
		}
		return 0
	default:
		fmt.Fprintln(stderr, "ctx: unsupported browser cookie operation")
		return 2
	}
}

func Encode(output io.Writer, value any) int {
	if err := json.NewEncoder(output).Encode(value); err != nil {
		return 1
	}
	return 0
}

func ReportError(stderr io.Writer, err error) int { return ReportErrorCode(stderr, err, 1) }
func ReportErrorCode(stderr io.Writer, err error, code int) int {
	fmt.Fprintf(stderr, "browser adapter: %v\n", err)
	return code
}
