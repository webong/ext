// Package share defines the bounded, transport-free model that mobile share and
// keyboard extensions use to hand text, links, and attachment references to
// their containing application.
//
// The package owns normalization and limits only. It never reads a native
// extension context, copies a file, selects a container, shows UI, records an
// event, launches an application, or applies a product protocol. Native
// implementations ship beside this contract in res/system/ios and
// res/system/android, and a caller supplies every destination directory.
package share

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultMaxItems bounds the number of contributions in one share event.
	DefaultMaxItems = 32
	// MaxItems is the library ceiling for MaxItems.
	MaxItems = 128
	// DefaultMaxBytes bounds attachment payload bytes plus declared sizes.
	DefaultMaxBytes int64 = 16 << 20
	// MaxBytes is the library ceiling for MaxBytes.
	MaxBytes int64 = 64 << 20
	// DefaultMaxTextBytes bounds text and subject content.
	DefaultMaxTextBytes = 64 << 10
	// MaxTextBytes is the library ceiling for MaxTextBytes.
	MaxTextBytes = 1 << 20
	// MaxURLLength bounds one normalized link.
	MaxURLLength = 8192
	// MaxNameLength bounds one attachment file name.
	MaxNameLength = 255
	// MaxMIMETypeLength bounds one attachment media type.
	MaxMIMETypeLength = 255
)

var (
	// ErrInvalid reports malformed or contradictory share input.
	ErrInvalid = errors.New("invalid share input")
	// ErrLimit reports input beyond a caller-selected bound.
	ErrLimit = errors.New("share limit exceeded")
)

// Limits are fixed by the embedding application before a native extension
// loads any item. Zero fields select the defaults; every field has a library
// ceiling so a caller cannot disable bounding.
type Limits struct {
	MaxItems     int   `json:"max_items"`
	MaxBytes     int64 `json:"max_bytes"`
	MaxTextBytes int   `json:"max_text_bytes"`
	MaxURLLength int   `json:"max_url_length"`
}

// Normalize resolves defaults and rejects limits beyond the library ceilings.
func (l Limits) Normalize() (Limits, error) {
	if l.MaxItems == 0 {
		l.MaxItems = DefaultMaxItems
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxTextBytes == 0 {
		l.MaxTextBytes = DefaultMaxTextBytes
	}
	if l.MaxURLLength == 0 {
		l.MaxURLLength = MaxURLLength
	}
	if l.MaxItems < 1 || l.MaxItems > MaxItems {
		return Limits{}, fmt.Errorf("%w: item bound must be between 1 and %d", ErrInvalid, MaxItems)
	}
	if l.MaxBytes < 1 || l.MaxBytes > MaxBytes {
		return Limits{}, fmt.Errorf("%w: byte bound must be between 1 and %d", ErrInvalid, MaxBytes)
	}
	if l.MaxTextBytes < 1 || l.MaxTextBytes > MaxTextBytes {
		return Limits{}, fmt.Errorf("%w: text bound must be between 1 and %d", ErrInvalid, MaxTextBytes)
	}
	if l.MaxURLLength < 1 || l.MaxURLLength > MaxURLLength {
		return Limits{}, fmt.Errorf("%w: URL bound must be between 1 and %d", ErrInvalid, MaxURLLength)
	}
	return l, nil
}

// Attachment is one shared file. Exactly one of Data and Path carries the
// content: Data is an already-bounded copy, Path is a caller-owned location
// outside the extension container. Size is the byte count of the content and
// must agree with Data when both are present.
type Attachment struct {
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Data     []byte `json:"data,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Item is one contribution from a native share source. Subject is optional
// preview text supplied by the sharing application; it is not a destination or
// an approval decision.
type Item struct {
	Subject     string       `json:"subject,omitempty"`
	Text        string       `json:"text,omitempty"`
	URLs        []string     `json:"urls,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Payload is the normalized, deduplicated result. ByteCount covers attachment
// content only; text has its own bound. The embedding application owns the
// destination and any further transport.
type Payload struct {
	Subject     string       `json:"subject,omitempty"`
	Text        string       `json:"text,omitempty"`
	URLs        []string     `json:"urls"`
	Attachments []Attachment `json:"attachments"`
	ByteCount   int64        `json:"byte_count"`
}

// Normalize validates and merges native items into one bounded payload. Exact
// duplicate texts, links, and attachments collapse to their first occurrence,
// so a provider that repeats an item cannot multiply an event. Conflicting
// duplicates such as two different attachments with one name are preserved
// with distinct names only when the caller can tell them apart; otherwise the
// second is rejected.
func Normalize(items []Item, limits Limits) (Payload, error) {
	limits, err := limits.Normalize()
	if err != nil {
		return Payload{}, err
	}
	if len(items) > limits.MaxItems {
		return Payload{}, fmt.Errorf("%w: %d items exceed the bound of %d", ErrLimit, len(items), limits.MaxItems)
	}
	payload := Payload{URLs: []string{}, Attachments: []Attachment{}}
	var subjects, texts []string
	subjectSeen := map[string]bool{}
	textSeen := map[string]bool{}
	urlSeen := map[string]bool{}
	attachmentSeen := map[string]bool{}
	attachmentNames := map[string]string{}
	var attachmentBytes int64
	for index, item := range items {
		if err := checkText(item.Subject, limits.MaxTextBytes); err != nil {
			return Payload{}, fmt.Errorf("item %d subject: %w", index, err)
		}
		if err := checkText(item.Text, limits.MaxTextBytes); err != nil {
			return Payload{}, fmt.Errorf("item %d text: %w", index, err)
		}
		if item.Subject != "" && !subjectSeen[item.Subject] {
			subjectSeen[item.Subject] = true
			subjects = append(subjects, item.Subject)
		}
		if item.Text != "" && !textSeen[item.Text] {
			textSeen[item.Text] = true
			texts = append(texts, item.Text)
		}
		if len(urlSeen)+len(item.URLs) > limits.MaxItems {
			return Payload{}, fmt.Errorf("%w: links exceed the bound of %d", ErrLimit, limits.MaxItems)
		}
		for _, raw := range item.URLs {
			link, err := normalizeURL(raw, limits.MaxURLLength)
			if err != nil {
				return Payload{}, fmt.Errorf("item %d: %w", index, err)
			}
			if urlSeen[link] {
				continue
			}
			urlSeen[link] = true
			payload.URLs = append(payload.URLs, link)
		}
		for _, attachment := range item.Attachments {
			normalized, err := normalizeAttachment(attachment)
			if err != nil {
				return Payload{}, fmt.Errorf("item %d: %w", index, err)
			}
			key := attachmentKey(normalized)
			if attachmentSeen[key] {
				continue
			}
			if previous, exists := attachmentNames[strings.ToLower(normalized.Name)]; exists && previous != key {
				return Payload{}, fmt.Errorf("%w: two different attachments share the name %q", ErrInvalid, normalized.Name)
			}
			attachmentSeen[key] = true
			attachmentNames[strings.ToLower(normalized.Name)] = key
			attachmentBytes += normalized.Size
			if attachmentBytes > limits.MaxBytes {
				return Payload{}, fmt.Errorf("%w: attachment bytes exceed the bound of %d", ErrLimit, limits.MaxBytes)
			}
			payload.Attachments = append(payload.Attachments, normalized)
		}
	}
	payload.Subject = strings.Join(subjects, "\n")
	payload.Text = strings.Join(texts, "\n")
	payload.ByteCount = attachmentBytes
	if len(payload.Subject) > limits.MaxTextBytes {
		return Payload{}, fmt.Errorf("%w: merged subject exceeds the bound of %d bytes", ErrLimit, limits.MaxTextBytes)
	}
	if len(payload.Text) > limits.MaxTextBytes {
		return Payload{}, fmt.Errorf("%w: merged text exceeds the bound of %d bytes", ErrLimit, limits.MaxTextBytes)
	}
	return payload, nil
}

func checkText(value string, limit int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: text must be valid UTF-8 without NUL", ErrInvalid)
	}
	if len(value) > limit {
		return fmt.Errorf("%w: text exceeds the bound of %d bytes", ErrLimit, limit)
	}
	return nil
}

func normalizeURL(raw string, limit int) (string, error) {
	link := strings.TrimSpace(raw)
	if link == "" {
		return "", fmt.Errorf("%w: link must not be empty", ErrInvalid)
	}
	if len(link) > limit {
		return "", fmt.Errorf("%w: link exceeds the bound of %d bytes", ErrLimit, limit)
	}
	if !utf8.ValidString(link) || strings.ContainsRune(link, 0) {
		return "", fmt.Errorf("%w: link must be valid UTF-8 without NUL", ErrInvalid)
	}
	for _, character := range link {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("%w: link must not contain control characters", ErrInvalid)
		}
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("%w: link must be an absolute URL", ErrInvalid)
	}
	return parsed.String(), nil
}

func normalizeAttachment(attachment Attachment) (Attachment, error) {
	name := strings.TrimSpace(attachment.Name)
	if name == "" || !utf8.ValidString(name) || len(name) > MaxNameLength {
		return Attachment{}, fmt.Errorf("%w: attachment requires a file name of at most %d bytes", ErrInvalid, MaxNameLength)
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.ContainsRune(name, 0) {
		return Attachment{}, fmt.Errorf("%w: attachment name %q is not a portable file name", ErrInvalid, name)
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return Attachment{}, fmt.Errorf("%w: attachment name must not contain control characters", ErrInvalid)
		}
	}
	mimeType := strings.TrimSpace(attachment.MIMEType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if len(mimeType) > MaxMIMETypeLength || strings.ContainsRune(mimeType, 0) {
		return Attachment{}, fmt.Errorf("%w: attachment media type is invalid", ErrInvalid)
	}
	if attachment.Data != nil && attachment.Path != "" {
		return Attachment{}, fmt.Errorf("%w: attachment carries both data and a path", ErrInvalid)
	}
	size := attachment.Size
	if attachment.Data != nil {
		if size != 0 && size != int64(len(attachment.Data)) {
			return Attachment{}, fmt.Errorf("%w: declared size disagrees with attachment data", ErrInvalid)
		}
		size = int64(len(attachment.Data))
	}
	if size < 0 {
		return Attachment{}, fmt.Errorf("%w: attachment size must not be negative", ErrInvalid)
	}
	if attachment.Path != "" && !strings.HasPrefix(attachment.Path, "/") {
		return Attachment{}, fmt.Errorf("%w: attachment path must be absolute", ErrInvalid)
	}
	return Attachment{
		Name: name, MIMEType: mimeType, Size: size,
		Data: attachment.Data, Path: attachment.Path,
	}, nil
}

func attachmentKey(attachment Attachment) string {
	content := attachment.Path
	if attachment.Data != nil {
		digest := sha256.Sum256(attachment.Data)
		content = hex.EncodeToString(digest[:])
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", strings.ToLower(attachment.Name), attachment.MIMEType, attachment.Size, content)
}
