package share

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeMergesAndDeduplicates(t *testing.T) {
	payload, err := Normalize([]Item{
		{Subject: "Release notes", Text: "first", URLs: []string{"https://example.test/a"}},
		{Text: "first", URLs: []string{"https://example.test/a", " HTTPS://example.test/b "}},
		{Subject: "Release notes", Text: "second", Attachments: []Attachment{
			{Name: "report.txt", MIMEType: "text/plain", Data: []byte("hello")},
		}},
	}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if payload.Subject != "Release notes" {
		t.Fatalf("subject = %q", payload.Subject)
	}
	if payload.Text != "first\nsecond" {
		t.Fatalf("text = %q", payload.Text)
	}
	if len(payload.URLs) != 2 || payload.URLs[0] != "https://example.test/a" || payload.URLs[1] != "https://example.test/b" {
		t.Fatalf("urls = %#v", payload.URLs)
	}
	if len(payload.Attachments) != 1 || payload.Attachments[0].Size != 5 || payload.ByteCount != 5 {
		t.Fatalf("attachments = %#v, bytes = %d", payload.Attachments, payload.ByteCount)
	}
	if payload.Attachments[0].MIMEType != "text/plain" {
		t.Fatalf("mime = %q", payload.Attachments[0].MIMEType)
	}
}

func TestNormalizeCollapsesDuplicateAttachments(t *testing.T) {
	attachment := Attachment{Name: "image.png", MIMEType: "image/png", Data: []byte("png")}
	payload, err := Normalize([]Item{
		{Attachments: []Attachment{attachment, attachment}},
		{Attachments: []Attachment{attachment}},
	}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Attachments) != 1 || payload.ByteCount != 3 {
		t.Fatalf("attachments = %#v, bytes = %d", payload.Attachments, payload.ByteCount)
	}
}

func TestNormalizeRejectsConflictingAttachmentNames(t *testing.T) {
	_, err := Normalize([]Item{{Attachments: []Attachment{
		{Name: "photo.jpg", MIMEType: "image/jpeg", Data: []byte("one")},
		{Name: "Photo.jpg", MIMEType: "image/jpeg", Data: []byte("two")},
	}}}, Limits{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("conflicting attachment names = %v", err)
	}
}

func TestNormalizeEnforcesByteCeiling(t *testing.T) {
	_, err := Normalize([]Item{{Attachments: []Attachment{
		{Name: "large.bin", Data: make([]byte, 64)},
	}}}, Limits{MaxBytes: 32})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized attachment = %v", err)
	}
}

func TestNormalizeCountsDeclaredAttachmentSize(t *testing.T) {
	_, err := Normalize([]Item{{Attachments: []Attachment{
		{Name: "stream.bin", Size: 1024, Path: "/tmp/stream.bin"},
	}}}, Limits{MaxBytes: 512})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("declared size = %v", err)
	}
}

func TestNormalizeEnforcesItemAndTextBounds(t *testing.T) {
	items := make([]Item, 3)
	items[0], items[1], items[2] = Item{Text: "a"}, Item{Text: "b"}, Item{Text: "c"}
	if _, err := Normalize(items, Limits{MaxItems: 2}); !errors.Is(err, ErrLimit) {
		t.Fatalf("item bound = %v", err)
	}
	if _, err := Normalize([]Item{{Text: strings.Repeat("x", 40)}}, Limits{MaxTextBytes: 16}); !errors.Is(err, ErrLimit) {
		t.Fatalf("item text bound = %v", err)
	}
	if _, err := Normalize([]Item{{Text: "0123456789"}, {Text: "abcdefghij"}}, Limits{MaxTextBytes: 12}); !errors.Is(err, ErrLimit) {
		t.Fatalf("merged text bound = %v", err)
	}
}

func TestNormalizeRejectsInvalidAttachments(t *testing.T) {
	for name, attachment := range map[string]Attachment{
		"traversal":    {Name: "../escape", Data: []byte("x")},
		"separator":    {Name: "nested/file", Data: []byte("x")},
		"empty":        {Name: "", Data: []byte("x")},
		"dotdot":       {Name: "..", Data: []byte("x")},
		"both":         {Name: "value", Data: []byte("x"), Path: "/tmp/value"},
		"relative":     {Name: "value", Path: "value"},
		"negative":     {Name: "value", Size: -1},
		"sizeMismatch": {Name: "value", Data: []byte("abc"), Size: 9},
	} {
		if _, err := Normalize([]Item{{Attachments: []Attachment{attachment}}}, Limits{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s attachment = %v", name, err)
		}
	}
}

func TestNormalizeRejectsInvalidLinksAndText(t *testing.T) {
	for name, item := range map[string]Item{
		"relativeLink": {URLs: []string{"not a url"}},
		"emptyLink":    {URLs: []string{"  "}},
		"controlLink":  {URLs: []string{"https://example.test/\x00"}},
		"nulText":      {Text: "value\x00"},
		"invalidUTF8":  {Text: string([]byte{0xff, 0xfe})},
	} {
		if _, err := Normalize([]Item{item}, Limits{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if _, err := Normalize([]Item{{URLs: []string{"https://example.test/" + strings.Repeat("a", 32)}}}, Limits{MaxURLLength: 16}); !errors.Is(err, ErrLimit) {
		t.Fatalf("link bound = %v", err)
	}
}

func TestLimitsNormalizeRejectsCeilings(t *testing.T) {
	for name, limits := range map[string]Limits{
		"items": {MaxItems: MaxItems + 1},
		"bytes": {MaxBytes: MaxBytes + 1},
		"text":  {MaxTextBytes: MaxTextBytes + 1},
		"url":   {MaxURLLength: MaxURLLength + 1},
		"zero":  {MaxItems: -1},
	} {
		if _, err := limits.Normalize(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s limits = %v", name, err)
		}
	}
	defaults, err := (Limits{}).Normalize()
	if err != nil || defaults.MaxItems != DefaultMaxItems || defaults.MaxBytes != DefaultMaxBytes {
		t.Fatalf("default limits = %#v, %v", defaults, err)
	}
}

func TestNormalizeEmptyInput(t *testing.T) {
	payload, err := Normalize(nil, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if payload.Text != "" || len(payload.URLs) != 0 || len(payload.Attachments) != 0 || payload.ByteCount != 0 {
		t.Fatalf("empty payload = %#v", payload)
	}
}
