package bundle

import "strings"

// Policy decides what the page may reach. The zero value is the strict one: the
// page can load and talk only to the origin that serves it.
type Policy struct {
	// AllowNet lets the page reach any network origin. Without it, a script
	// cannot fetch, open a socket to, or load a resource from anywhere else.
	AllowNet bool
	// AllowOrigins lists specific extra origins (for example
	// "https://api.example.com") for connections, without allowing everything.
	// It has no effect when AllowNet is true.
	AllowOrigins []string
}

// ContentSecurityPolicy renders the header every response carries. Inline
// scripts and WebAssembly compilation stay allowed because bundles need them;
// what is closed is the network.
//
// A CSP cannot stop a page from navigating the whole window to another address,
// so this limits what a page can load and send but is not a complete sandbox.
// Run bundles you do not trust in a dedicated browser profile.
func (p Policy) ContentSecurityPolicy() string {
	connect, resources := "'self'", "'self' data: blob:"
	if p.AllowNet {
		connect, resources = "*", "* data: blob:"
	} else if len(p.AllowOrigins) > 0 {
		extra := strings.Join(sanitizeOrigins(p.AllowOrigins), " ")
		connect = "'self' " + extra
		resources = "'self' data: blob: " + extra
	}
	directives := []string{
		"default-src 'none'",
		"script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval' blob:",
		"style-src 'self' 'unsafe-inline'",
		"connect-src " + connect,
		"img-src " + resources,
		"font-src " + resources,
		"media-src " + resources,
		"worker-src 'self' blob:",
		"form-action 'none'",
		"frame-src 'none'",
		"object-src 'none'",
		"base-uri 'self'",
	}
	return strings.Join(directives, "; ")
}

// sanitizeOrigins keeps only plain http(s) origins, so a caller cannot smuggle
// another directive through an allowed-origin string.
func sanitizeOrigins(origins []string) []string {
	var kept []string
	for _, origin := range origins {
		if !(strings.HasPrefix(origin, "https://") || strings.HasPrefix(origin, "http://")) {
			continue
		}
		if strings.ContainsAny(origin, " ;,'\"\r\n\t") || strings.Count(origin, "/") != 2 {
			continue
		}
		kept = append(kept, origin)
	}
	return kept
}
