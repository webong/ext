package browser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	browsercontract "github.com/webong/ext/res/browser/contract"
)

// NormalizeOptions binds an authorized native export to one adapter endpoint.
type NormalizeOptions struct {
	Source  string // explicit browser:profile, bound by the exporting application
	StoreID string // explicit native store selected by the exporting application
	Export  json.RawMessage
	Timeout time.Duration
	Backend Backend
}

// Normalize delegates native cookie export interpretation to the supplied
// backend. Its Result can be serialized for Inline or FallbackInline.
// It does not obtain browser permissions or verify a file's runtime provenance.
func Normalize(ctx context.Context, options NormalizeOptions) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("cookie normalization needs a context")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	name, profile, ok := strings.Cut(options.Source, ":")
	control := func(r rune) bool { return r < 0x20 || r == 0x7f }
	if !ok || name == "" || profile == "" || strings.ContainsFunc(profile, control) || options.StoreID == "" ||
		strings.ContainsFunc(options.StoreID, control) || len(options.StoreID) > 256 || options.Timeout < 0 {
		return Result{}, errors.New("cookie normalization needs an explicit browser:profile, store ID, and nonnegative timeout")
	}
	if len(options.Export) > MaxCookieInputBytes || !utf8.Valid(options.Export) || !json.Valid(options.Export) {
		return Result{}, errors.New("native cookie export must be bounded UTF-8 JSON")
	}
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	if options.Backend == nil {
		return Result{}, errors.New("cookie normalization needs a source backend")
	}
	request, err := json.Marshal(browsercontract.CookieRequest{Version: browsercontract.Version, NativeExport: options.Export, StoreID: options.StoreID})
	if err != nil || len(request) > MaxCookieInputBytes {
		return Result{}, errors.New("native cookie normalization request exceeds the input limit")
	}
	output, err := options.Backend.Normalize(ctx, name, profile, request)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var response struct {
		StoreID  string   `json:"store_id"`
		Warnings []string `json:"warnings"`
	}
	if json.Unmarshal(output, &response) != nil || response.StoreID != options.StoreID {
		return Result{}, errors.New("adapter returned an invalid normalized store binding")
	}
	cookies, err := ParseCookies(output)
	if err != nil {
		return Result{}, err
	}
	for index := range cookies {
		cookies[index].Source = options.Source
		cookies[index].SourceInfo = SourceInfo{Adapter: name, Profile: profile, StoreID: options.StoreID}
	}
	return Result{Cookies: cookies, Warnings: response.Warnings}, nil
}
