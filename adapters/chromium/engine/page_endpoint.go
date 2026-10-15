package chromium

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"time"

	management "github.com/webong/ext/res/web/contract"
)

// EndpointProbeTimeout bounds the loopback connection that confirms a browser is
// still listening behind a debugging marker. The marker file survives a crash, so
// without the probe a stale marker would yield a dead endpoint. Tests may shorten
// it.
var EndpointProbeTimeout = 2 * time.Second

// Endpoint answers session.endpoint: the browser-level CDP endpoint for a
// profile, read from the Chromium-owned debugging marker, without needing an
// open page. The probe only connects to the marker's loopback port and closes
// the connection; it sends no data and follows no redirects. Errors never include
// the endpoint, whose path is a secret.
func (backend extensionBackend) Endpoint(ctx context.Context, profile string, raw json.RawMessage) (management.EndpointResult, error) {
	if backend.config.Extensions.ExtensionPage == "" {
		return management.EndpointResult{}, errors.New("this adapter has no CDP page route")
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var none struct{}
		if err := decoder.Decode(&none); err != nil {
			return management.EndpointResult{}, errors.New("session.endpoint takes no input")
		}
	}
	endpoint, err := (&cdpPageRuntime{config: backend.config}).endpointFor(profile)
	if err != nil {
		return management.EndpointResult{}, err
	}
	if err := probeEndpoint(ctx, endpoint); err != nil {
		return management.EndpointResult{}, err
	}
	return management.EndpointResult{Protocol: "cdp", Endpoint: endpoint}, nil
}

func probeEndpoint(ctx context.Context, endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" || parsed.Port() == "" {
		return errors.New("invalid debugging endpoint")
	}
	dialer := net.Dialer{Timeout: EndpointProbeTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(parsed.Hostname(), parsed.Port()))
	if err != nil {
		return errors.New("no active debugging endpoint for this profile: nothing is listening")
	}
	return connection.Close()
}
