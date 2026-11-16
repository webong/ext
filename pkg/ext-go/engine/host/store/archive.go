package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// DefaultMaxArchiveBytes bounds a package archive, as downloaded or as a local
// file, when Limits does not.
const DefaultMaxArchiveBytes int64 = 512 << 20

// MaxDownloadRedirects is how many redirects NewDownloadClient follows.
const MaxDownloadRedirects = 5

// HTTPClient is what Download needs from an HTTP client. *http.Client is one.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// NewDownloadClient returns the client Download uses when it is given none. It
// follows at most MaxDownloadRedirects redirects, each to an HTTPS URL without
// user information or a fragment, and removes Authorization, Cookie and Referer
// from every redirected request. It has no timeout of its own: the context
// passed to Download bounds the transfer.
func NewDownloadClient() *http.Client {
	return &http.Client{CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) > MaxDownloadRedirects {
			return fmt.Errorf("%w: download exceeded %d redirects", plugin.ErrInvalid, MaxDownloadRedirects)
		}
		if err := checkDownloadURL(request.URL); err != nil {
			return err
		}
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Referer")
		return nil
	}}
}

func checkDownloadURL(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("%w: download requires an HTTPS URL without user information or fragment", plugin.ErrInvalid)
	}
	return nil
}

// Download fetches source, an HTTPS URL without user information or a fragment,
// into the new file destination, which must not exist. It fails when the body
// is larger than maxBytes (DefaultMaxArchiveBytes when zero) and leaves no file
// behind on failure. A nil client uses NewDownloadClient. A caller's own client
// owns its redirect policy; the response is still refused when it did not come
// from an HTTPS URL. The content is not verified: check it against a pinned
// digest, as ExtractArchive does, before using it.
func Download(ctx context.Context, client HTTPClient, source, destination string, maxBytes int64) error {
	if maxBytes < 0 || destination == "" {
		return plugin.ErrInvalid
	}
	if maxBytes == 0 {
		maxBytes = DefaultMaxArchiveBytes
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return fmt.Errorf("%w: download URL does not parse", plugin.ErrInvalid)
	}
	if err := checkDownloadURL(parsed); err != nil {
		return err
	}
	if client == nil {
		client = NewDownloadClient()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer response.Body.Close()
	if response.Request != nil {
		if err := checkDownloadURL(response.Request.URL); err != nil {
			return err
		}
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download: origin returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return fmt.Errorf("%w: download exceeds size limit", plugin.ErrInvalid)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, maxBytes+1))
	closeErr := file.Close()
	if copyErr == nil && closeErr == nil && written > maxBytes {
		copyErr = fmt.Errorf("%w: download exceeds size limit", plugin.ErrInvalid)
	}
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		return errors.Join(copyErr, closeErr)
	}
	return nil
}

// SafeArchivePath reports whether name is a slash-separated relative path that
// an archive entry may use on every supported platform: no empty, "." or ".."
// element, no backslash, colon or control character, no element ending in a dot
// or space, and no Windows device name.
func SafeArchivePath(name string) bool {
	if name == "" || name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:") {
		return false
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if len(part) > 255 || strings.TrimRight(part, ". ") != part {
			return false
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" ||
			len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
			return false
		}
	}
	return true
}

func normalizeSHA256(value string) (string, error) {
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("%w: SHA-256 must contain 64 hexadecimal characters", plugin.ErrInvalid)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("%w: SHA-256 must contain 64 hexadecimal characters", plugin.ErrInvalid)
	}
	return strings.ToLower(value), nil
}

// ExtractArchive checks the archive file against its pinned SHA-256 and
// extracts it into destination, which must not exist or must be an empty
// directory. It returns the package root inside destination: destination itself
// when ManifestName is at the top of the archive, or the single top-level
// directory that holds it.
//
// The archive is a zip, a gzip-compressed tar or a plain tar, told apart by
// content and not by name; anything else is plugin.ErrUnsupported. Only directories and regular files are extracted; a link, a device
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
	if limits.MaxArchiveBytes < 0 || limits.MaxArtifactBytes < 0 || limits.MaxTreeFiles < 0 || limits.MaxTreeBytes < 0 || destination == "" {
		return "", plugin.ErrInvalid
	}
	limits = limits.normalize()
	want := ""
	if sha256Hex != "" {
		var err error
		if want, err = normalizeSHA256(sha256Hex); err != nil {
			return "", err
		}
	}
	file, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: archive is not a regular file", plugin.ErrInvalid)
	}
	if info.Size() > limits.MaxArchiveBytes {
		return "", fmt.Errorf("%w: archive exceeds size limit", plugin.ErrInvalid)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, limits.MaxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if size > limits.MaxArchiveBytes {
		return "", fmt.Errorf("%w: archive exceeds size limit", plugin.ErrInvalid)
	}
	if want != "" && hex.EncodeToString(hash.Sum(nil)) != want {
		return "", fmt.Errorf("%w: archive SHA-256 does not match", plugin.ErrMismatch)
	}
	head := make([]byte, 512)
	n, err := file.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	format, err := detectArchive(head[:n])
	if err != nil {
		return "", err
	}
	if entries, err := os.ReadDir(destination); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("%w: extraction destination is not empty", plugin.ErrInvalid)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", err
	}
	x := &extraction{root: destination, limits: limits, seen: map[string]bool{}}
	if err := format.read(file, size, x.entry); err != nil {
		return "", err
	}
	return archiveRoot(destination)
}

// extraction writes validated entries under root and keeps the running bounds.
type extraction struct {
	root    string
	limits  Limits
	seen    map[string]bool // lower-cased path -> is a directory
	entries int
	bytes   int64
}

// claim validates one entry name and records it. skip is true for the archive's
// own top directory, which needs nothing.
func (x *extraction) claim(raw string, dir bool) (target string, skip bool, err error) {
	name := strings.TrimSuffix(raw, "/")
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	if dir && (name == "." || name == "") {
		return "", true, nil
	}
	if !SafeArchivePath(name) {
		return "", false, fmt.Errorf("%w: unsafe archive path %q", plugin.ErrInvalid, raw)
	}
	x.entries++
	if x.entries > x.limits.MaxTreeFiles {
		return "", false, fmt.Errorf("%w: archive exceeds its file limit", plugin.ErrInvalid)
	}
	key := strings.ToLower(name)
	if wasDir, ok := x.seen[key]; ok {
		if dir && wasDir {
			return "", true, nil
		}
		return "", false, fmt.Errorf("%w: duplicate archive path %q", plugin.ErrInvalid, raw)
	}
	x.seen[key] = dir
	// The directories above an entry exist once it does, listed or not, so a
	// later file may not take the place of one, nor an entry sit under a file.
	for parent := key; ; {
		cut := strings.LastIndexByte(parent, '/')
		if cut < 0 {
			break
		}
		parent = parent[:cut]
		if wasDir, ok := x.seen[parent]; ok && !wasDir {
			return "", false, fmt.Errorf("%w: archive path %q is under a file", plugin.ErrInvalid, raw)
		}
		x.seen[parent] = true
	}
	target = filepath.Join(x.root, filepath.FromSlash(name))
	if relative, err := filepath.Rel(x.root, target); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("%w: archive path %q escapes the destination", plugin.ErrInvalid, raw)
	}
	return target, false, nil
}

func (x *extraction) directory(raw string) error {
	target, skip, err := x.claim(raw, true)
	if err != nil || skip {
		return err
	}
	return os.MkdirAll(target, 0o700)
}

// regular writes one file of exactly the declared size, which must fit the
// bounds.
func (x *extraction) regular(raw string, mode fs.FileMode, declared int64, content io.Reader) error {
	target, _, err := x.claim(raw, false)
	if err != nil {
		return err
	}
	remaining := x.limits.MaxTreeBytes - x.bytes
	allowed := min(x.limits.MaxArtifactBytes, remaining)
	if declared < 0 || declared > allowed {
		return fmt.Errorf("%w: archive exceeds its size limits", plugin.ErrInvalid)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	perm := fs.FileMode(0o600)
	if mode.Perm()&0o100 != 0 {
		perm = 0o700
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	body := &bodyReader{source: content}
	written, copyErr := io.Copy(out, io.LimitReader(body, declared+1))
	closeErr := out.Close()
	if body.err != nil {
		return fmt.Errorf("%w: reading archive entry %q: %v", plugin.ErrInvalid, raw, body.err)
	}
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if written != declared {
		return fmt.Errorf("%w: archive entry %q does not have its declared size", plugin.ErrInvalid, raw)
	}
	x.bytes += written
	return nil
}

// bodyReader remembers a failure to read an entry's body, which is a defect of
// the archive, apart from a failure to write the file.
type bodyReader struct {
	source io.Reader
	err    error
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.source.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.err = err
	}
	return n, err
}

// entry extracts one entry of any format.
func (x *extraction) entry(e archiveEntry) error {
	switch e.kind {
	case entryDirectory:
		return x.directory(e.name)
	case entryFile:
		content, err := e.open()
		if err != nil {
			return err
		}
		err = x.regular(e.name, e.mode, e.size, content)
		return errors.Join(err, content.Close())
	}
	return fmt.Errorf("%w: archive entry %q is a link or special file", plugin.ErrInvalid, e.name)
}

// archiveRoot finds the package directory in an extracted archive.
func archiveRoot(extracted string) (string, error) {
	if info, err := os.Lstat(filepath.Join(extracted, ManifestName)); err == nil && info.Mode().IsRegular() {
		return extracted, nil
	}
	entries, err := os.ReadDir(extracted)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return "", fmt.Errorf("%w: archive must hold %s at its top or in one top-level directory", plugin.ErrInvalid, ManifestName)
	}
	root := filepath.Join(extracted, entries[0].Name())
	if info, err := os.Lstat(filepath.Join(root, ManifestName)); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: archive must hold %s at its top or in one top-level directory", plugin.ErrInvalid, ManifestName)
	}
	return root, nil
}
