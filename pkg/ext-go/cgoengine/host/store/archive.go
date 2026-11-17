package store

import (
	"context"
	"net/http"
	"strings"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	cstore "github.com/webong/ext/pkg/ext-go/engine/host/store"
)

// Archive extraction and download do not go through the engine: they are the
// pure-Go store's, so there is one extractor and one downloader. What they
// extract is installed through this package's engine-routed Store.

// DefaultMaxArchiveBytes bounds a package archive, as downloaded or as a local
// file, when Limits does not.
const DefaultMaxArchiveBytes int64 = cstore.DefaultMaxArchiveBytes

// MaxDownloadRedirects is how many redirects NewDownloadClient follows.
const MaxDownloadRedirects = cstore.MaxDownloadRedirects

// HTTPClient is what Download needs from an HTTP client. *http.Client is one.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// NewDownloadClient returns the client Download uses when it is given none. It
// follows at most MaxDownloadRedirects redirects, each to an HTTPS URL without
// user information or a fragment, and removes Authorization, Cookie and Referer
// from every redirected request. It has no timeout of its own: the context
// passed to Download bounds the transfer.
func NewDownloadClient() *http.Client { return cstore.NewDownloadClient() }

// Download fetches source, an HTTPS URL without user information or a fragment,
// into the new file destination, which must not exist. It fails when the body
// is larger than maxBytes (DefaultMaxArchiveBytes when zero) and leaves no file
// behind on failure. A nil client uses NewDownloadClient. A caller's own client
// owns its redirect policy; the response is still refused when it did not come
// from an HTTPS URL. The content is not verified: check it against a pinned
// digest, as ExtractArchive does, before using it.
func Download(ctx context.Context, client HTTPClient, source, destination string, maxBytes int64) error {
	return cstore.Download(ctx, client, source, destination, maxBytes)
}

// SafeArchivePath reports whether name is a slash-separated relative path that
// an archive entry may use on every supported platform: no empty, "." or ".."
// element, no backslash, colon or control character, no element ending in a dot
// or space, and no Windows device name.
func SafeArchivePath(name string) bool { return cstore.SafeArchivePath(name) }

// ExtractArchive checks the archive file against its pinned SHA-256 and
// extracts it into destination, which must not exist or must be an empty
// directory. It returns the package root inside destination: destination itself
// when ManifestName is at the top of the archive, or the single top-level
// directory that holds it.
//
// The archive is a zip, a gzip-compressed tar or a plain tar, told apart by
// content and not by name; anything else is plugin.ErrUnsupported. A zip in
// the zip64 form and a tar that stops inside a block are refused. Only directories and regular files are extracted; a link, a device
// or any other special entry fails the extraction, as does a path that is
// absolute, leaves the destination, repeats another (whatever its case) or is
// not a SafeArchivePath. limits bounds the work: MaxArchiveBytes the archive
// file, MaxTreeFiles its entries, MaxTreeBytes the extracted total and
// MaxArtifactBytes one file. Files are written 0600, or 0700 when the entry has
// its owner-execute bit; nothing else of an entry's metadata is kept.
//
// An empty sha256 skips the digest check, for an archive whose bytes the caller
// has already authenticated. Nothing is validated beyond finding the manifest:
// Install verifies the package. On failure destination may hold a partial tree.
func ExtractArchive(archive, sha256Hex, destination string, limits Limits) (string, error) {
	return cstore.ExtractArchive(archive, sha256Hex, destination, cstore.Limits{
		MaxArtifactBytes: limits.MaxArtifactBytes, MaxTreeFiles: limits.MaxTreeFiles,
		MaxTreeBytes: limits.MaxTreeBytes, MaxArchiveBytes: limits.MaxArchiveBytes,
	})
}

func normalizeSHA256(value string) (string, error) {
	if len(value) != 64 {
		return "", plugin.ErrInvalid
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return "", plugin.ErrInvalid
		}
	}
	return strings.ToLower(value), nil
}
