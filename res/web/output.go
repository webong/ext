package web

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	browsercontract "github.com/webong/ext/res/web/contract"
)

// CookieHeader builds the value of an HTTP Cookie header for one request URL.
// It filters expired and out-of-scope cookies, preserves duplicate names at
// different paths, and orders longer paths first. Native isolation cannot be
// represented in a header and is rejected rather than silently removed.
func CookieHeader(cookies []Cookie, site string) (string, error) {
	parsed, err := browsercontract.ParseSite(site)
	if err != nil {
		return "", err
	}
	selected := make([]Cookie, 0, len(cookies))
	for index, cookie := range cookies {
		if err := browsercontract.ValidateCookie(cookie.Cookie); err != nil {
			return "", fmt.Errorf("cookie %d: %w", index+1, err)
		}
		if !browsercontract.CookieMatchesSite(parsed, cookie.Cookie) {
			continue
		}
		if err := portableCookieScope(cookie); err != nil {
			return "", fmt.Errorf("cookie %d: %w", index+1, err)
		}
		for _, character := range []byte(cookie.Value) {
			// RFC 6265 cookie-octet: no whitespace, quotes, comma,
			// semicolon, backslash, controls, or non-ASCII bytes.
			if !(character == 0x21 || character >= 0x23 && character <= 0x2b ||
				character >= 0x2d && character <= 0x3a || character >= 0x3c && character <= 0x5b ||
				character >= 0x5d && character <= 0x7e) {
				return "", fmt.Errorf("cookie %d: value cannot be represented in an HTTP Cookie header", index+1)
			}
		}
		selected = append(selected, cookie)
	}
	sort.SliceStable(selected, func(i, j int) bool { return len(selected[i].Path) > len(selected[j].Path) })
	parts := make([]string, 0, len(selected))
	for _, cookie := range selected {
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; "), nil
}

// NetscapeCookies exports a curl-compatible jar. This format preserves domain,
// path, Secure, HttpOnly, expiry, and values; it cannot retain SameSite or source
// metadata. Partitioned cookies and adapter-owned scopes require CTX JSON.
func NetscapeCookies(cookies []Cookie) (string, error) {
	var output strings.Builder
	output.WriteString("# Netscape HTTP Cookie File\n# Exported by CTX; SameSite and source metadata are not represented.\n")
	for index, cookie := range cookies {
		if err := browsercontract.ValidateCookie(cookie.Cookie); err != nil {
			return "", fmt.Errorf("cookie %d: %w", index+1, err)
		}
		if err := portableCookieScope(cookie); err != nil {
			return "", fmt.Errorf("cookie %d: %w", index+1, err)
		}
		if strings.ContainsAny(cookie.Value, "\t\r\n") {
			return "", fmt.Errorf("cookie %d: value cannot be represented in a Netscape jar", index+1)
		}
		domain := cookie.Domain
		if cookie.HTTPOnly {
			domain = "#HttpOnly_" + domain
		}
		expiry := cookie.Expiry
		if expiry < 0 {
			expiry = 1 // an expired row must not become a session cookie
		}
		fields := []string{domain, jarBool(strings.HasPrefix(cookie.Domain, ".")), cookie.Path,
			jarBool(cookie.Secure), strconv.FormatInt(expiry, 10), cookie.Name, cookie.Value}
		output.WriteString(strings.Join(fields, "\t"))
		output.WriteByte('\n')
	}
	return output.String(), nil
}

func portableCookieScope(cookie Cookie) error {
	if cookie.PartitionKey != "" || cookie.CrossSiteAncestor || len(cookie.Attributes) > 0 {
		return errors.New("native cookie isolation cannot be represented in this format; use CTX JSON")
	}
	return nil
}

func jarBool(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}
