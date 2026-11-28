package cookie

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/webong/ext/src/ctx/web/contract"
)

var ErrNoMatch = errors.New("browser cookie: no matching cookie")
var ErrAmbiguous = errors.New("browser cookie: multiple cookies match")

// Share calls one cookie operation on an already selected and authorized
// browser backend. The backend owns native storage and scope preservation.
type Share func(context.Context, string, contract.CookieRequest, any) error

// Selector identifies one listed cookie without relying on its secret value.
type Selector struct {
	Name       string
	Domain     string
	Path       string
	ID         int64
	Ref        string
	Attributes map[string]string
}

// ListForSite obtains portable cookie metadata and filters it for a site. A
// listed value is always erased before it reaches the caller.
func ListForSite(ctx context.Context, source Share, site *url.URL) ([]contract.Cookie, error) {
	if ctx == nil || source == nil || site == nil {
		return nil, errors.New("browser cookie list needs a context, source, and site")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var listed []contract.Cookie
	if err := source(ctx, "list", contract.CookieRequest{Version: contract.Version, Site: site.String()}, &listed); err != nil {
		return nil, err
	}
	filtered := make([]contract.Cookie, 0, len(listed))
	for _, item := range listed {
		item.Value = ""
		if err := contract.ValidateCookie(item); err != nil {
			return nil, fmt.Errorf("source adapter returned invalid cookie metadata: %w", err)
		}
		if contract.CookieMatchesSite(site, item) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// Select requires exactly one match among metadata returned by ListForSite.
func Select(listed []contract.Cookie, selector Selector) (contract.Cookie, error) {
	var match contract.Cookie
	count := 0
	for _, item := range listed {
		if item.Name != selector.Name || selector.Domain != "" && item.Domain != selector.Domain ||
			selector.Path != "" && item.Path != selector.Path || selector.ID != 0 && item.ID != selector.ID ||
			selector.Ref != "" && item.Ref != selector.Ref || !matchesAttributes(item.Attributes, selector.Attributes) {
			continue
		}
		match = item
		count++
	}
	if count == 0 {
		return contract.Cookie{}, ErrNoMatch
	}
	if count != 1 {
		return contract.Cookie{}, ErrAmbiguous
	}
	match.Value = ""
	return match, nil
}

func matchesAttributes(actual, wanted map[string]string) bool {
	for key, value := range wanted {
		if got, ok := actual[key]; !ok || got != value {
			return false
		}
	}
	return true
}

// ExportSelected rechecks that the exported cookie is the one selected from
// metadata, then validates the portable bundle before returning its secret.
func ExportSelected(ctx context.Context, source Share, sourceLabel string, site *url.URL, selected contract.Cookie) (contract.CookieBundle, error) {
	if ctx == nil || source == nil || site == nil || sourceLabel == "" {
		return contract.CookieBundle{}, errors.New("browser cookie export needs a context, source, label, and site")
	}
	if err := ctx.Err(); err != nil {
		return contract.CookieBundle{}, err
	}
	selected.Value = ""
	var exported contract.Cookie
	if err := source(ctx, "export", contract.CookieRequest{Version: contract.Version, Site: site.String(), Cookie: selected}, &exported); err != nil {
		return contract.CookieBundle{}, err
	}
	if !contract.SameListedCookie(exported, selected) {
		return contract.CookieBundle{}, errors.New("source adapter returned a different cookie than selected")
	}
	bundle := contract.CookieBundle{Version: contract.Version, Source: sourceLabel, Site: BundleSite(site), Cookie: exported}
	if err := contract.ValidateCookieBundle(bundle); err != nil {
		return contract.CookieBundle{}, err
	}
	return bundle, nil
}

// BundleSite removes query and fragment fields from the site in an export.
func BundleSite(site *url.URL) string {
	if site == nil {
		return ""
	}
	return (&url.URL{Scheme: site.Scheme, Host: site.Host, Path: site.Path, RawPath: site.RawPath}).String()
}

// Import validates the bundle before passing it to the destination backend.
func Import(ctx context.Context, destination Share, bundle contract.CookieBundle, replace bool) error {
	if ctx == nil || destination == nil {
		return errors.New("browser cookie import needs a context and destination")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := contract.ValidateCookieBundle(bundle); err != nil {
		return err
	}
	return destination(ctx, "import", contract.CookieRequest{Version: contract.Version, Bundle: &bundle, Replace: replace}, nil)
}

// Copy exports a selected cookie, verifies it, then imports the bundle.
func Copy(ctx context.Context, source, destination Share, sourceLabel string, site *url.URL, selected contract.Cookie, replace bool) (contract.CookieBundle, error) {
	bundle, err := ExportSelected(ctx, source, sourceLabel, site, selected)
	if err != nil {
		return contract.CookieBundle{}, err
	}
	if err := Import(ctx, destination, bundle, replace); err != nil {
		return contract.CookieBundle{}, err
	}
	return bundle, nil
}
