// Package bookmarklet prepares portable, user-activated browser scripts. It
// never executes source or modifies a browser's bookmark database.
package bookmarklet

import (
	"errors"
	"html"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxSourceBytes bounds preparation; browser bookmark URL limits may be lower.
const MaxSourceBytes = 64 * 1024

func validate(source string) error {
	if strings.TrimSpace(source) == "" {
		return errors.New("bookmarklet source is empty")
	}
	if len(source) > MaxSourceBytes {
		return errors.New("bookmarklet source exceeds 64 KiB")
	}
	if !utf8.ValidString(source) || strings.ContainsRune(source, 0) {
		return errors.New("bookmarklet source must be UTF-8 without NUL bytes")
	}
	return nil
}

// Encode wraps a JavaScript function body so string results cannot replace the
// document. Source is percent-encoded, retaining newlines and literal percent
// signs. Syntax and page permissions are checked by the browser at activation.
func Encode(source string) (string, error) {
	if err := validate(source); err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "javascript:") {
		return "", errors.New("expected JavaScript source; import a javascript: URL first")
	}
	code := "void (function(){\n" + source + "\n}).call(window);"
	// QueryEscape encodes spaces as '+', which has no special meaning in a
	// javascript: URL. Encode spaces explicitly instead.
	return "javascript:" + strings.ReplaceAll(url.QueryEscape(code), "+", "%20"), nil
}

// Decode imports a javascript: URL as source for review, without evaluating it.
// Decode exactly once and retain literal '+' and non-escape '%' characters,
// matching URL percent decoding (including JavaScript's modulo operator).
func Decode(value string) (string, error) {
	value = strings.TrimSpace(value)
	const prefix = "javascript:"
	if len(value) > 3*(MaxSourceBytes+64)+len(prefix) {
		return "", errors.New("bookmarklet URL is too large")
	}
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", errors.New("bookmarklet URL must use javascript:")
	}
	body := value[len(prefix):]
	var decoded strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '%' && i+2 < len(body) {
			if b, err := strconv.ParseUint(body[i+1:i+3], 16, 8); err == nil {
				decoded.WriteByte(byte(b))
				i += 2
				continue
			}
		}
		decoded.WriteByte(body[i])
	}
	source := decoded.String()
	// Remove only our exact export wrapper for lossless round trips.
	const start, end = "void (function(){\n", "\n}).call(window);"
	if strings.HasPrefix(source, start) && strings.HasSuffix(source, end) {
		source = strings.TrimSuffix(strings.TrimPrefix(source, start), end)
	}
	if err := validate(source); err != nil {
		return "", err
	}
	return source, nil
}

// InstallPage returns a standalone HTML page with a draggable bookmark link
// and escaped source for review. There is no script that runs on page load.
func InstallPage(name, source string) (string, error) {
	if strings.TrimSpace(name) == "" || len(name) > 256 || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return "", errors.New("bookmarklet name must be nonempty UTF-8, at most 256 bytes")
	}
	link, err := Encode(source)
	if err != nil {
		return "", err
	}
	return "<!doctype html>\n<html lang=\"en\"><meta charset=\"utf-8\"><title>Install " + html.EscapeString(name) + "</title>\n" +
		"<h1>Install " + html.EscapeString(name) + "</h1>\n" +
		"<p>Review the source, then drag this link to your bookmarks bar. Open the target page and click the saved bookmark.</p>\n" +
		"<p><a id=\"bookmarklet\" draggable=\"true\" href=\"" + html.EscapeString(link) + "\">" + html.EscapeString(name) + "</a></p>\n" +
		"<p>No extension process is needed. The script runs with the target page's access. Browser restrictions or the page's content security policy may prevent execution.</p>\n" +
		"<h2>Source</h2><pre>" + html.EscapeString(source) + "</pre></html>\n", nil
}
