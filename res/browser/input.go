package browser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	browsercontract "github.com/webong/ctx/res/browser/contract"
)

const MaxCookieInputBytes = 8 << 20

// ParseCookies normalizes a ctx cookie bundle, query result, JSON cookie array,
// or Netscape cookie jar. It preserves native scope fields and rejects formats
// whose store or partition scope needs interpretation by a browser adapter.
// Parsing errors identify fields or row numbers, never secret values.
func ParseCookies(data []byte) ([]Cookie, error) {
	if len(data) > MaxCookieInputBytes {
		return nil, errors.New("cookie input exceeds 8 MiB")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("cookie input must be UTF-8")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("cookie input is empty")
	}
	if trimmed[0] != '[' && trimmed[0] != '{' {
		return parseNetscapeCookies(data)
	}
	data = trimmed
	var rows []json.RawMessage
	if data[0] == '[' {
		if err := json.Unmarshal(data, &rows); err != nil {
			return nil, errors.New("invalid cookie JSON array")
		}
	} else {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, errors.New("invalid cookie JSON object")
		}
		if object["cookie"] != nil && object["cookies"] != nil {
			return nil, errors.New("cookie input cannot contain both cookie and cookies")
		}
		switch {
		case object["cookie"] != nil:
			var version int
			if err := json.Unmarshal(object["version"], &version); err != nil || version != browsercontract.Version {
				return nil, errors.New("unsupported cookie bundle version")
			}
			rows = []json.RawMessage{object["cookie"]}
		case object["cookies"] != nil:
			if bytes.Equal(object["cookies"], []byte("null")) || json.Unmarshal(object["cookies"], &rows) != nil {
				return nil, errors.New("cookie input needs a cookies array")
			}
		default:
			return nil, errors.New("cookie input must be an array, query result, or versioned bundle")
		}
		if object["cookie"] != nil {
			cookie, err := parseJSONCookie(rows[0])
			if err != nil {
				return nil, fmt.Errorf("cookie 1: %w", err)
			}
			if raw := object["source"]; raw != nil && json.Unmarshal(raw, &cookie.Source) != nil {
				return nil, errors.New("invalid cookie bundle source")
			}
			return []Cookie{cookie}, nil
		}
	}
	cookies := make([]Cookie, 0, len(rows))
	for index, row := range rows {
		cookie, err := parseJSONCookie(row)
		if err != nil {
			return nil, fmt.Errorf("cookie %d: %w", index+1, err)
		}
		cookies = append(cookies, cookie)
	}
	return cookies, nil
}

func parseJSONCookie(data []byte) (Cookie, error) {
	if !utf8.Valid(data) {
		return Cookie{}, errors.New("cookie JSON must be UTF-8")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return Cookie{}, errors.New("expected a cookie object")
	}
	// Unknown fields can encode native scope. Do not silently discard them.
	allowed := map[string]bool{}
	for _, name := range strings.Fields("id ref name value domain path expiry secure http_only same_site_policy partition_key has_cross_site_ancestor attributes source source_info hostOnly httpOnly expirationDate expires session sameSite storeId firstPartyDomain partitionKey") {
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return Cookie{}, errors.New("cookie contains an unsupported field; use an adapter to normalize its scope")
		}
	}
	for _, name := range []string{"name", "value", "domain", "path"} {
		var value string
		if fields[name] == nil || bytes.Equal(fields[name], []byte("null")) || json.Unmarshal(fields[name], &value) != nil {
			return Cookie{}, fmt.Errorf("cookie needs a string %s", name)
		}
	}
	for _, name := range []string{"storeId", "firstPartyDomain"} {
		if raw := fields[name]; raw != nil {
			var scope string
			if json.Unmarshal(raw, &scope) != nil || scope != "" {
				return Cookie{}, errors.New("cookie store scope needs adapter normalization")
			}
		}
	}
	if raw := fields["partitionKey"]; raw != nil && !bytes.Equal(raw, []byte("null")) {
		return Cookie{}, errors.New("cookie partition scope needs adapter normalization; use partition_key or namespaced attributes")
	}
	for _, pair := range [][2]string{{"httpOnly", "http_only"}, {"sameSite", "same_site_policy"}} {
		if fields[pair[0]] != nil {
			if fields[pair[1]] != nil {
				return Cookie{}, errors.New("cookie contains conflicting field aliases")
			}
			fields[pair[1]] = fields[pair[0]]
		}
	}
	for _, name := range []string{"secure", "http_only", "has_cross_site_ancestor", "same_site_policy", "partition_key"} {
		if bytes.Equal(fields[name], []byte("null")) {
			return Cookie{}, errors.New("portable cookie fields cannot be null")
		}
	}
	var expirySeen bool
	for _, name := range []string{"expiry", "expirationDate", "expires"} {
		if raw := fields[name]; raw != nil {
			if expirySeen {
				return Cookie{}, errors.New("cookie contains conflicting expiry fields")
			}
			expirySeen = true
			var seconds float64
			if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &seconds) != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > 253402300799 || seconds < -1 {
				return Cookie{}, errors.New("cookie has an invalid expiry")
			}
			// Common JSON exports use expires=-1 for a session cookie.
			if name == "expires" && seconds == -1 {
				seconds = 0
			}
			expiry := int64(math.Floor(seconds))
			if seconds > 0 && expiry == 0 {
				expiry = -1 // expired subsecond timestamp must not become a session
			}
			fields["expiry"] = json.RawMessage(strconv.FormatInt(expiry, 10))
		}
	}
	canonical, _ := json.Marshal(fields)
	var cookie Cookie
	if json.Unmarshal(canonical, &cookie) != nil {
		return Cookie{}, errors.New("invalid portable cookie fields")
	}
	if raw := fields["hostOnly"]; raw != nil {
		var hostOnly bool
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &hostOnly) != nil {
			return Cookie{}, errors.New("cookie has an invalid hostOnly field")
		}
		cookie.Domain = strings.TrimPrefix(cookie.Domain, ".")
		if !hostOnly {
			cookie.Domain = "." + cookie.Domain
		}
	}
	if raw := fields["session"]; raw != nil {
		var session bool
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &session) != nil || (session && cookie.Expiry != 0) || (!session && cookie.Expiry == 0) {
			return Cookie{}, errors.New("cookie session and expiry fields disagree")
		}
	}
	cookie.SameSitePolicy = strings.ToLower(cookie.SameSitePolicy)
	if cookie.SameSitePolicy == "no_restriction" {
		cookie.SameSitePolicy = "none"
	}
	if err := browsercontract.ValidateCookie(cookie.Cookie); err != nil {
		return Cookie{}, err
	}
	return cookie, nil
}

func parseNetscapeCookies(data []byte) ([]Cookie, error) {
	cookies := make([]Cookie, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), MaxCookieInputBytes)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSuffix(scanner.Text(), "\r")
		httpOnly := strings.HasPrefix(text, "#HttpOnly_")
		if text == "" || (strings.HasPrefix(text, "#") && !httpOnly) {
			continue
		}
		text = strings.TrimPrefix(text, "#HttpOnly_")
		fields := strings.Split(text, "\t")
		if len(fields) != 7 {
			return nil, fmt.Errorf("Netscape cookie line %d needs seven tab-separated fields", line)
		}
		subdomains, secure := fields[1] == "TRUE", fields[3] == "TRUE"
		if (fields[1] != "TRUE" && fields[1] != "FALSE") || (fields[3] != "TRUE" && fields[3] != "FALSE") {
			return nil, fmt.Errorf("Netscape cookie line %d has an invalid boolean", line)
		}
		expiry, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || expiry < 0 {
			return nil, fmt.Errorf("Netscape cookie line %d has an invalid expiry", line)
		}
		domain := strings.TrimPrefix(fields[0], ".")
		if subdomains {
			domain = "." + domain
		}
		cookie := Cookie{Cookie: browsercontract.Cookie{Name: fields[5], Value: fields[6], Domain: domain, Path: fields[2], Expiry: expiry, Secure: secure, HTTPOnly: httpOnly}}
		if err := browsercontract.ValidateCookie(cookie.Cookie); err != nil {
			return nil, fmt.Errorf("Netscape cookie line %d: %w", line, err)
		}
		cookies = append(cookies, cookie)
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("Netscape cookie line exceeds input limit")
	}
	return cookies, nil
}

// BundleCookie creates a validated single-cookie import bundle from a query
// or authorized export. The caller must choose the cookie and request site.
func BundleCookie(cookie Cookie, site string) (browsercontract.CookieBundle, error) {
	parsed, err := browsercontract.ParseSite(site)
	if err != nil {
		return browsercontract.CookieBundle{}, err
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	parsed.ForceQuery = false
	source := cookie.Source
	if source == "" {
		source = "inline"
	}
	bundle := browsercontract.CookieBundle{Version: browsercontract.Version, Source: source, Site: parsed.String(), Cookie: cookie.Cookie}
	return bundle, browsercontract.ValidateCookieBundle(bundle)
}
