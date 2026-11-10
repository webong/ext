package share_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/webong/ext/res/system/capture/share"
	"github.com/webong/ext/res/system/internal/fixtures"
)

type limitsSpec struct {
	MaxItems     *int   `json:"maxItems"`
	MaxBytes     *int64 `json:"maxBytes"`
	MaxTextBytes *int   `json:"maxTextBytes"`
	MaxURLLength *int   `json:"maxUrlLength"`
}

// limits fills a field a case leaves out with the default, since Go cannot tell zero from absent.
func (l *limitsSpec) limits() share.Limits {
	var out share.Limits
	if l == nil {
		return out
	}
	if l.MaxItems != nil {
		out.MaxItems = *l.MaxItems
	}
	if l.MaxBytes != nil {
		out.MaxBytes = *l.MaxBytes
	}
	if l.MaxTextBytes != nil {
		out.MaxTextBytes = *l.MaxTextBytes
	}
	if l.MaxURLLength != nil {
		out.MaxURLLength = *l.MaxURLLength
	}
	return out
}

type attachmentSpec struct {
	Name     string  `json:"name"`
	MIMEType string  `json:"mimeType"`
	Size     int64   `json:"size"`
	Text     *string `json:"text"`
	Zeros    *int    `json:"zeros"`
	Path     string  `json:"path"`
}

func (a attachmentSpec) attachment() share.Attachment {
	out := share.Attachment{Name: a.Name, MIMEType: a.MIMEType, Size: a.Size, Path: a.Path}
	switch {
	case a.Text != nil:
		out.Data = []byte(*a.Text)
	case a.Zeros != nil:
		out.Data = make([]byte, *a.Zeros)
	}
	return out
}

type itemSpec struct {
	Subject     string           `json:"subject"`
	Text        string           `json:"text"`
	URLs        []string         `json:"urls"`
	Attachments []attachmentSpec `json:"attachments"`
}

type payloadSpec struct {
	Subject     string           `json:"subject"`
	Text        string           `json:"text"`
	URLs        []string         `json:"urls"`
	Attachments []attachmentSpec `json:"attachments"`
	ByteCount   int64            `json:"byteCount"`
}

type shareCases struct {
	Defaults struct {
		MaxItems     int   `json:"maxItems"`
		MaxBytes     int64 `json:"maxBytes"`
		MaxTextBytes int   `json:"maxTextBytes"`
		MaxURLLength int   `json:"maxUrlLength"`
	} `json:"defaults"`
	Limits []struct {
		Name   string     `json:"name"`
		Limits limitsSpec `json:"limits"`
		Code   *string    `json:"code"`
	} `json:"limits"`
	Normalize []struct {
		Name    string       `json:"name"`
		Limits  *limitsSpec  `json:"limits"`
		Items   []itemSpec   `json:"items"`
		Code    *string      `json:"code"`
		Payload *payloadSpec `json:"payload"`
	} `json:"normalize"`
}

func expectCode(t *testing.T, codes map[string]bool, want *string, err error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		return
	}
	if !codes[*want] {
		t.Fatalf("code %q is not in testdata/codes.json", *want)
	}
	if err == nil {
		t.Fatalf("accepted, want %s", *want)
	}
	if got := share.Code(err); got != *want {
		t.Fatalf("code %q (%v), want %s", got, err, *want)
	}
}

// TestFixtures runs testdata/share/v1, which the Swift and Java bindings run as well.
func TestFixtures(t *testing.T) {
	var cases shareCases
	fixtures.Load(t, "share", 1, &cases)
	codes := fixtures.Codes(t)
	if len(cases.Limits) == 0 || len(cases.Normalize) == 0 {
		t.Fatal("no cases")
	}
	t.Run("defaults", func(t *testing.T) {
		got, err := (share.Limits{}).Normalize()
		want := share.Limits{MaxItems: cases.Defaults.MaxItems, MaxBytes: cases.Defaults.MaxBytes, MaxTextBytes: cases.Defaults.MaxTextBytes, MaxURLLength: cases.Defaults.MaxURLLength}
		if err != nil || got != want {
			t.Fatalf("%+v, %v; want %+v", got, err, want)
		}
	})
	for _, c := range cases.Limits {
		t.Run("limits/"+c.Name, func(t *testing.T) {
			_, err := c.Limits.limits().Normalize()
			expectCode(t, codes, c.Code, err)
		})
	}
	for _, c := range cases.Normalize {
		t.Run("normalize/"+c.Name, func(t *testing.T) {
			var items []share.Item
			for _, i := range c.Items {
				item := share.Item{Subject: i.Subject, Text: i.Text, URLs: i.URLs}
				for _, a := range i.Attachments {
					item.Attachments = append(item.Attachments, a.attachment())
				}
				items = append(items, item)
			}
			got, err := share.Normalize(items, c.Limits.limits())
			expectCode(t, codes, c.Code, err)
			if c.Code != nil {
				return
			}
			want := c.Payload
			if got.Subject != want.Subject || got.Text != want.Text || got.ByteCount != want.ByteCount {
				t.Fatalf("payload %q %q %d, want %q %q %d", got.Subject, got.Text, got.ByteCount, want.Subject, want.Text, want.ByteCount)
			}
			if !reflect.DeepEqual(got.URLs, append([]string{}, want.URLs...)) {
				t.Fatalf("urls %q, want %q", got.URLs, want.URLs)
			}
			if len(got.Attachments) != len(want.Attachments) {
				t.Fatalf("%d attachments, want %d", len(got.Attachments), len(want.Attachments))
			}
			for i, a := range want.Attachments {
				w := a.attachment()
				mime := w.MIMEType
				g := got.Attachments[i]
				if g.Name != w.Name || g.MIMEType != mime || g.Size != w.Size || g.Path != w.Path || !bytes.Equal(g.Data, w.Data) {
					t.Fatalf("attachment %d: %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

func TestCodeIsEmptyForOtherErrors(t *testing.T) {
	if got := share.Code(errors.New("native failure")); got != "" {
		t.Fatalf("code %q", got)
	}
}
