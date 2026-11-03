package contract

import (
	"encoding/json"
	"errors"
	"maps"
	"net"
	"net/url"
	"strings"
	"time"
)

func ParseSite(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("site must be an http or https URL with a hostname")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed, nil
}

func CookieActive(cookie Cookie) bool {
	return cookie.Expiry == 0 || cookie.Expiry > time.Now().Unix()
}

func CookieDomainMatches(siteHost, cookieDomain string) bool {
	siteHost = strings.TrimSuffix(strings.ToLower(siteHost), ".")
	cookieDomain = strings.ToLower(cookieDomain)
	if strings.HasPrefix(cookieDomain, ".") {
		base := strings.TrimPrefix(cookieDomain, ".")
		return siteHost == base || strings.HasSuffix(siteHost, "."+base)
	}
	return siteHost == cookieDomain
}

// CookiePathMatches follows the path-match rule used when browsers send a
// cookie. A prefix is not enough unless the next path character is '/'.
func CookiePathMatches(requestPath, cookiePath string) bool {
	if requestPath == "" {
		requestPath = "/"
	}
	if cookiePath == "" || !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return requestPath == cookiePath || strings.HasSuffix(cookiePath, "/") || requestPath[len(cookiePath)] == '/'
}

func CookieMatchesSite(site *url.URL, cookie Cookie) bool {
	return CookieMatchesSiteOptions(site, cookie, false)
}

func CookieMatchesSiteOptions(site *url.URL, cookie Cookie, includeExpired bool) bool {
	if site == nil {
		return includeExpired || CookieActive(cookie)
	}
	return CookieDomainMatches(site.Hostname(), cookie.Domain) &&
		(site.Path == "" || CookiePathMatches(site.EscapedPath(), cookie.Path)) &&
		(!cookie.Secure || site.Scheme == "https") && (includeExpired || CookieActive(cookie))
}

func SameListedCookie(a, b Cookie) bool {
	return a.ID == b.ID && a.Ref == b.Ref && a.Name == b.Name && a.Domain == b.Domain && a.Path == b.Path &&
		a.PartitionKey == b.PartitionKey && a.CrossSiteAncestor == b.CrossSiteAncestor && maps.Equal(a.Attributes, b.Attributes)
}

func ValidateCookieBundle(bundle CookieBundle) error {
	if bundle.Version != Version {
		return errors.New("unsupported browser cookie bundle version")
	}
	site, err := ParseSite(bundle.Site)
	if err != nil {
		return err
	}
	cookie := bundle.Cookie
	if err := ValidateCookie(cookie); err != nil {
		return err
	}
	if !CookieMatchesSite(site, cookie) {
		return errors.New("cookie bundle has an invalid or expired site scope")
	}
	switch cookie.SameSitePolicy {
	case "", "unspecified", "none", "lax", "strict":
	default:
		return errors.New("cookie bundle has an unsupported SameSite policy")
	}
	return nil
}

// ValidateCookie checks portable fields without requiring an unexpired cookie
// or a particular site. Errors never contain the cookie's secret value.
func ValidateCookie(cookie Cookie) error {
	if cookie.Name == "" || strings.ContainsAny(cookie.Name, "()<>@,;:\\\"/[]?={} \t") || hasControl(cookie.Name) {
		return errors.New("cookie has an invalid name")
	}
	host := strings.TrimPrefix(cookie.Domain, ".")
	if host == "" || strings.ContainsAny(host, " /\\?#@\t\r\n") || hasControl(host) ||
		(strings.Contains(host, ":") && net.ParseIP(host) == nil) || strings.HasPrefix(host, ".") {
		return errors.New("cookie has an invalid domain")
	}
	if !strings.HasPrefix(cookie.Path, "/") || hasControl(cookie.Path) || hasControl(cookie.Value) {
		return errors.New("cookie has an invalid path or value")
	}
	if (strings.HasPrefix(cookie.Name, "__Secure-") && !cookie.Secure) ||
		(strings.HasPrefix(cookie.Name, "__Host-") && (!cookie.Secure || strings.HasPrefix(cookie.Domain, ".") || cookie.Path != "/")) ||
		(cookie.SameSitePolicy == "none" && !cookie.Secure) {
		return errors.New("cookie violates secure prefix or SameSite requirements")
	}
	// This upper bound also keeps native microsecond expiry conversions safe.
	if cookie.Expiry > 253402300799 {
		return errors.New("cookie expiry exceeds the portable range")
	}
	if hasControl(cookie.PartitionKey) || (cookie.CrossSiteAncestor && cookie.PartitionKey == "") {
		return errors.New("cookie has an invalid partition scope")
	}
	if len(cookie.Attributes) > 32 {
		return errors.New("cookie has too many adapter attributes")
	}
	for key, value := range cookie.Attributes {
		if !strings.Contains(key, ".") || len(key) > 128 || len(value) > 1024 || strings.ContainsAny(key, " \t\r\n") {
			return errors.New("cookie has an invalid adapter attribute")
		}
	}
	return nil
}

func hasControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func ValidateResourceBundle(bundle ResourceBundle, resource string) error {
	if bundle.Version != Version || bundle.Resource != resource || len(bundle.Payload) == 0 || !json.Valid(bundle.Payload) {
		return errors.New("resource bundle has an invalid version, type, or payload")
	}
	return nil
}
