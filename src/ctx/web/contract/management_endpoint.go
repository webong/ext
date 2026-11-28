package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// MaxEndpointBytes bounds an endpoint URL, and MaxEndpointResultBytes bounds the
// whole result of session.endpoint.
const (
	MaxEndpointBytes       = 2048
	MaxEndpointResultBytes = 4096
)

// EndpointResult is the result of session.endpoint: the browser-level debugging
// endpoint for a profile, available without an open page.
//
// The URL is a capability. For CDP, the path holds a secret identifier, and
// anyone who can read the URL and reach its port controls every profile and page
// of that browser process. Treat the response like a credential: do not log it,
// persist it, or forward the port. String and GoString print a redacted form so
// that %v, %+v and %#v cannot leak it; JSON encoding carries the real value
// because the protocol needs it.
type EndpointResult struct {
	Protocol string `json:"protocol"` // cdp or bidi
	Endpoint string `json:"endpoint"`
}

// String returns a redacted description, safe for logs and errors.
func (r EndpointResult) String() string {
	return fmt.Sprintf("%s endpoint %s", r.Protocol, RedactEndpoint(r.Endpoint))
}

// GoString returns the same redacted form for %#v.
func (r EndpointResult) GoString() string { return r.String() }

// RedactEndpoint keeps the scheme, host and port of an endpoint URL and drops
// everything else, including the secret path. An unparsable value is replaced
// entirely.
func RedactEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "<redacted>"
	}
	return parsed.Scheme + "://" + parsed.Host + "/<redacted>"
}

// ValidateEndpoint checks an EndpointResult. The protocol must be cdp or bidi.
// The URL must use ws or wss, carry no credentials, query or fragment, stay
// within MaxEndpointBytes, and name a loopback host: an adapter reports only
// endpoints on the local machine, and a host must refuse anything else. Error
// text is fixed and never includes the URL.
func ValidateEndpoint(result EndpointResult) error {
	if result.Protocol != "cdp" && result.Protocol != "bidi" {
		return errors.New("endpoint protocol must be cdp or bidi")
	}
	if result.Endpoint == "" || len(result.Endpoint) > MaxEndpointBytes {
		return errors.New("endpoint URL is empty or too long")
	}
	parsed, err := url.Parse(result.Endpoint)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" || parsed.Hostname() == "" {
		return errors.New("endpoint must be a WebSocket URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.ContainsRune(result.Endpoint, '#') {
		return errors.New("endpoint must not carry credentials, a query, or a fragment")
	}
	if !loopbackHost(parsed.Hostname()) {
		return errors.New("endpoint must be on a loopback address")
	}
	if port := parsed.Port(); port == "" {
		return errors.New("endpoint needs an explicit port")
	}
	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateEndpointResponse(response Response) error {
	if len(response.Result) > MaxEndpointResultBytes {
		return errors.New("endpoint response result exceeds its bound")
	}
	var result EndpointResult
	decoder := json.NewDecoder(strings.NewReader(string(response.Result)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return errors.New("endpoint response result is not an endpoint")
	}
	return ValidateEndpoint(result)
}
