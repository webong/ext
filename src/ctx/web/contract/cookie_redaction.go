package contract

import (
	"fmt"
	"strings"
)

// MaxCookieQueryResultBytes bounds one encoded cookie.query result. An adapter
// refuses to return more, and a host reading an adapter's stdout should stop at
// this size. Narrow the site or names instead of raising it.
const MaxCookieQueryResultBytes = 32 << 20

// Cookie values are credentials. The String and GoString methods below print a
// cookie's identity (name, domain and path) and never its value, so that
// %v, %+v, %#v and %s on a cookie, or on a bundle, request or result holding
// one, cannot put a value in a log or an error. JSON encoding keeps the real
// value because the protocol needs it; treat that output as secret.

// String describes the cookie without its value.
func (c Cookie) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cookie{name=%q domain=%q path=%q", c.Name, c.Domain, c.Path)
	if c.Value != "" {
		b.WriteString(" value=<redacted>")
	}
	if c.PartitionKey != "" {
		fmt.Fprintf(&b, " partition=%q", c.PartitionKey)
	}
	b.WriteString("}")
	return b.String()
}

// GoString matches String for %#v.
func (c Cookie) GoString() string { return c.String() }

// String describes the bundle; the cookie's value is redacted.
func (b CookieBundle) String() string {
	return fmt.Sprintf("cookie bundle{version=%d source=%q site=%q %s}", b.Version, b.Source, b.Site, b.Cookie)
}

// GoString matches String for %#v.
func (b CookieBundle) GoString() string { return b.String() }

// String describes the query result by count; each cookie is printed without
// its value. Warnings are printed as they are: adapters must not put values in
// them.
func (r CookieQueryResult) String() string {
	parts := make([]string, 0, len(r.Cookies))
	for _, c := range r.Cookies {
		parts = append(parts, c.String())
	}
	return fmt.Sprintf("cookie result{cookies=[%s] warnings=%d store=%q}", strings.Join(parts, " "), len(r.Warnings), r.StorePath)
}

// GoString matches String for %#v.
func (r CookieQueryResult) GoString() string { return r.String() }

// String describes the request. The cookie value, any bundle value and the
// native export are never printed; only the size of the export is.
func (r CookieRequest) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cookie request{version=%d site=%q names=%q expired=%t all_hosts=%t replace=%t", r.Version, r.Site, r.Names, r.IncludeExpired, r.AllowAllHosts, r.Replace)
	if r.Cookie.Name != "" || r.Cookie.Value != "" {
		b.WriteString(" " + r.Cookie.String())
	}
	if r.Bundle != nil {
		b.WriteString(" " + r.Bundle.String())
	}
	if len(r.NativeExport) > 0 {
		fmt.Fprintf(&b, " native_export=<%d bytes redacted>", len(r.NativeExport))
	}
	b.WriteString("}")
	return b.String()
}

// GoString matches String for %#v.
func (r CookieRequest) GoString() string { return r.String() }
