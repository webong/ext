// Package browser provides portable cookie parsing, queries, and normalization.
// Hosts supply authorized sources through Backend; adapters own native storage
// formats and operating-system credentials.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/webong/ctx/res/browser/contract"
)

// Mode controls how results from ordered sources are combined.
type Mode string

const (
	ModeMerge Mode = "merge"
	ModeFirst Mode = "first"
)

// InlineCookies supplies local JSON cookies or a Netscape cookie jar.
// Exactly one field may be set. Options.Inline is tried before adapters;
// Options.FallbackInline is tried after them.
type InlineCookies struct {
	// Data accepts JSON or a Netscape cookie jar. JSON is retained for callers
	// supplying the original JSON-only API; both are parsed as cookie input.
	Data   []byte
	JSON   []byte
	Base64 string
	File   string
}

// Options selects sites, adapters, and profiles. Sources contains explicit
// browser:profile endpoints in priority order. Browsers may contain adapter
// names and uses Profiles to select one profile per adapter; otherwise all
// discoverable profiles are used. The backend owns source discovery and trust.
// A URL or Origins entry is required unless AllowAllHosts is explicitly set.
type Options struct {
	URL      string
	Origins  []string
	Names    []string
	Sources  []string
	Browsers []string
	Profiles map[string]string
	// PreferredSource is a browser:profile endpoint tried first during automatic discovery.
	PreferredSource string
	Mode            Mode
	Inline          InlineCookies
	// FallbackInline is read after adapters and fills scopes they could not read.
	// In ModeFirst it is used only when earlier sources returned no matching cookies.
	FallbackInline InlineCookies
	InlineOnly     bool
	IncludeExpired bool
	AllowAllHosts  bool
	Timeout        time.Duration
	// Backend supplies trusted sources. CTX provides one; other hosts can
	// implement the same boundary without importing CTX internals.
	Backend Backend
}

// Source is a browser endpoint selected and authorized by a host. The
// resource workflow handles portable cookie requests and result merging.
type Source struct {
	Adapter       string
	Profile       string
	Label         string
	SupportsQuery bool
	Invoke        func(context.Context, string, []string, []byte) ([]byte, error)
}

// Backend owns adapter discovery, trust, capability checks, and dispatch.
// Resources deliberately do not import CTX's adapter store.
type Backend interface {
	Sources(context.Context, Options) ([]Source, []string, error)
	Normalize(context.Context, string, string, []byte) ([]byte, error)
}

// Cookie includes its portable browser fields and the endpoint that supplied it.
type Cookie struct {
	contract.Cookie
	Source     string     `json:"source"`
	SourceInfo SourceInfo `json:"source_info"`
}

// SourceInfo describes the adapter and profile that supplied a cookie.
// StorePath is available when an adapter reports its on-disk cookie store.
type SourceInfo struct {
	Adapter   string `json:"adapter,omitempty"`
	Profile   string `json:"profile,omitempty"`
	StoreID   string `json:"store_id,omitempty"`
	StorePath string `json:"store_path,omitempty"`
	Inline    bool   `json:"inline,omitempty"`
	Fallback  bool   `json:"fallback,omitempty"`
}

// Result retains partial success when an unavailable source reports a warning.
type Result struct {
	Cookies  []Cookie `json:"cookies"`
	Warnings []string `json:"warnings,omitempty"`
}

// Get retrieves and combines cookies from sources authorized by the supplied
// backend. Inline-only queries do not need a backend.
func Get(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("browser query needs a context")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if options.Mode != "" && options.Mode != ModeMerge && options.Mode != ModeFirst {
		return Result{}, fmt.Errorf("unknown browser query mode %q", options.Mode)
	}
	if options.Timeout < 0 {
		return Result{}, errors.New("browser query timeout cannot be negative")
	}
	if len(options.Sources) > 0 && len(options.Browsers) > 0 {
		return Result{}, errors.New("choose Sources or Browsers")
	}
	if options.InlineOnly && (len(options.Sources) > 0 || len(options.Browsers) > 0) {
		return Result{}, errors.New("InlineOnly cannot be combined with browser sources")
	}
	sites, err := querySites(options)
	if err != nil {
		return Result{}, err
	}
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	result := Result{Cookies: make([]Cookie, 0)}
	seen := map[string]bool{}
	inline, err := parseInline(options.Inline)
	if err != nil {
		return Result{}, err
	}
	fallback, err := parseInline(options.FallbackInline)
	if err != nil {
		return Result{}, fmt.Errorf("fallback cookies: %w", err)
	}
	appendInlineCookies(&result, seen, inline, sites, options, "inline")
	if options.Mode == ModeFirst && len(result.Cookies) > 0 {
		return result, nil
	}
	if options.InlineOnly {
		if options.Mode != ModeFirst || len(result.Cookies) == 0 {
			appendInlineCookies(&result, seen, fallback, sites, options, "inline:fallback")
		}
		return result, nil
	}
	if options.Backend == nil {
		return Result{}, errors.New("browser query needs a source backend")
	}
	sources, warnings, err := options.Backend.Sources(ctx, options)
	if err != nil {
		return Result{}, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(sites) == 0 {
		sites = []*url.URL{nil}
	}
	for _, source := range sources {
		if source.Adapter == "" || source.Profile == "" || source.Label == "" || source.Invoke == nil {
			return result, errors.New("browser backend returned an invalid source")
		}
		before := len(result.Cookies)
		for _, site := range sites {
			cookies, warnings, storePath, err := sourceCookies(ctx, source, site, options)
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			for _, cookie := range cookies {
				if matchesQuery(cookie, sites, options) {
					appendCookie(&result, seen, Cookie{Cookie: cookie, Source: source.Label,
						SourceInfo: SourceInfo{Adapter: source.Adapter, Profile: source.Profile, StorePath: storePath}})
				}
			}
			for _, warning := range warnings {
				result.Warnings = append(result.Warnings, source.Label+": "+warning)
			}
			if err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", source.Label, err))
				continue
			}
		}
		if options.Mode == ModeFirst && len(result.Cookies) > before {
			break
		}
	}
	if options.Mode != ModeFirst || len(result.Cookies) == 0 {
		appendInlineCookies(&result, seen, fallback, sites, options, "inline:fallback")
	}
	return result, nil
}

func appendInlineCookies(result *Result, seen map[string]bool, cookies []contract.Cookie, sites []*url.URL, options Options, label string) {
	for _, cookie := range cookies {
		if matchesQuery(cookie, sites, options) {
			appendCookie(result, seen, Cookie{Cookie: cookie, Source: label,
				SourceInfo: SourceInfo{Inline: true, Fallback: label == "inline:fallback"}})
		}
	}
}

func querySites(options Options) ([]*url.URL, error) {
	var raw []string
	if options.URL != "" {
		raw = append(raw, options.URL)
	}
	raw = append(raw, options.Origins...)
	if len(raw) == 0 && !options.AllowAllHosts {
		return nil, errors.New("browser query needs URL or Origins")
	}
	sites := make([]*url.URL, 0, len(raw))
	for _, value := range raw {
		site, err := contract.ParseSite(value)
		if err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	return sites, nil
}

func parseInline(input InlineCookies) ([]contract.Cookie, error) {
	count := 0
	for _, set := range []bool{input.Data != nil, input.JSON != nil, input.Base64 != "", input.File != ""} {
		if set {
			count++
		}
	}
	if count > 1 {
		return nil, errors.New("inline cookies accept exactly one of Data, JSON, Base64, or File")
	}
	data := input.JSON
	if input.Data != nil {
		data = input.Data
	}
	if input.Base64 != "" {
		if len(input.Base64) > base64.StdEncoding.EncodedLen(8<<20) {
			return nil, errors.New("inline cookies exceed 8 MiB")
		}
		var err error
		data, err = base64.StdEncoding.DecodeString(input.Base64)
		if err != nil {
			return nil, fmt.Errorf("decode inline cookies: %w", err)
		}
	}
	if input.File != "" {
		file, err := os.Open(input.File)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return nil, errors.New("inline cookie file must be a regular file under 8 MiB")
		}
		data, err = io.ReadAll(io.LimitReader(file, (8<<20)+1))
		if err != nil {
			return nil, err
		}
	}
	if data == nil {
		return nil, nil
	}
	if len(data) > 8<<20 {
		return nil, errors.New("inline cookies exceed 8 MiB")
	}
	parsed, err := ParseCookies(data)
	if err != nil {
		return nil, err
	}
	cookies := make([]contract.Cookie, 0, len(parsed))
	for _, cookie := range parsed {
		cookies = append(cookies, cookie.Cookie)
	}
	return cookies, nil
}

func matchesQuery(cookie contract.Cookie, sites []*url.URL, options Options) bool {
	if cookie.Name == "" || cookie.Domain == "" || !strings.HasPrefix(cookie.Path, "/") {
		return false
	}
	if !options.IncludeExpired && !contract.CookieActive(cookie) {
		return false
	}
	if len(options.Names) > 0 {
		found := false
		for _, name := range options.Names {
			if cookie.Name == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(sites) == 0 {
		return options.AllowAllHosts
	}
	for _, site := range sites {
		if site == nil {
			return options.AllowAllHosts
		}
		if contract.CookieDomainMatches(site.Hostname(), cookie.Domain) &&
			(site.Path == "" || contract.CookiePathMatches(site.EscapedPath(), cookie.Path)) &&
			(!cookie.Secure || site.Scheme == "https") {
			return true
		}
	}
	return false
}

func appendCookie(result *Result, seen map[string]bool, cookie Cookie) {
	// Adapter attributes are opaque. Their complete, canonical map contributes
	// to identity so any adapter can preserve native cookie scopes.
	attributes := ""
	if len(cookie.Attributes) > 0 {
		encoded, _ := json.Marshal(cookie.Attributes) // map[string]string cannot fail to encode
		attributes = string(encoded)
	}
	encoded, _ := json.Marshal([]any{cookie.Name, cookie.Domain, cookie.Path, cookie.PartitionKey, cookie.CrossSiteAncestor, attributes})
	key := string(encoded)
	if seen[key] {
		return
	}
	seen[key] = true
	result.Cookies = append(result.Cookies, cookie)
}

func sourceCookies(ctx context.Context, source Source, site *url.URL, options Options) ([]contract.Cookie, []string, string, error) {
	if source.SupportsQuery {
		request := contract.CookieRequest{Version: contract.Version, Names: options.Names,
			IncludeExpired: options.IncludeExpired, AllowAllHosts: site == nil}
		if site != nil {
			request.Site = site.String()
		}
		payload, _ := json.Marshal(request)
		output, err := source.Invoke(ctx, "share", []string{"cookie", "query"}, payload)
		if err != nil {
			return nil, nil, "", err
		}
		var result struct {
			Cookies   []json.RawMessage `json:"cookies"`
			Warnings  []string          `json:"warnings"`
			StorePath string            `json:"store_path"`
		}
		if err := json.Unmarshal(output, &result); err != nil {
			return nil, nil, "", errors.New("invalid cookie query response")
		}
		if result.Cookies == nil {
			return nil, nil, "", errors.New("cookie query response needs a cookies array")
		}
		cookies := make([]contract.Cookie, 0, len(result.Cookies))
		for index, row := range result.Cookies {
			cookie, err := parseJSONCookie(row)
			if err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("cookie %d: %v", index+1, err))
				continue
			}
			cookies = append(cookies, cookie.Cookie)
		}
		return cookies, result.Warnings, result.StorePath, nil
	}
	if site == nil || options.IncludeExpired {
		return nil, nil, "", errors.New("adapter does not support all-host or expired-cookie queries")
	}
	request, _ := json.Marshal(contract.CookieRequest{Version: contract.Version, Site: site.String()})
	output, err := source.Invoke(ctx, "share", []string{"cookie", "list"}, request)
	if err != nil {
		return nil, nil, "", err
	}
	var listed []contract.Cookie
	if err := json.Unmarshal(output, &listed); err != nil {
		return nil, nil, "", errors.New("invalid cookie list response")
	}
	var cookies []contract.Cookie
	var warnings []string
	for _, cookie := range listed {
		if len(options.Names) > 0 {
			found := false
			for _, name := range options.Names {
				if name == cookie.Name {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if !contract.CookieMatchesSite(site, cookie) {
			continue
		}
		request, _ := json.Marshal(contract.CookieRequest{Version: contract.Version, Site: site.String(), Cookie: cookie})
		output, err := source.Invoke(ctx, "share", []string{"cookie", "export"}, request)
		if err != nil {
			if ctx.Err() != nil {
				return cookies, warnings, "", ctx.Err()
			}
			warnings = append(warnings, fmt.Sprintf("export %s: %v", cookie.Name, err))
			continue
		}
		exported, err := parseJSONCookie(output)
		if err != nil || !contract.SameListedCookie(cookie, exported.Cookie) {
			warnings = append(warnings, fmt.Sprintf("export %s returned an invalid or different cookie", cookie.Name))
			continue
		}
		cookies = append(cookies, exported.Cookie)
	}
	return cookies, warnings, "", nil
}
