package clipboard_test

import (
	"errors"
	"testing"

	"github.com/webong/ext/res/system/capture/clipboard"
	"github.com/webong/ext/res/system/internal/fixtures"
)

type fixtureRepresentation struct {
	Format string  `json:"format"`
	Text   *string `json:"text"`
	Zeros  int     `json:"zeros"`
}

type fixtureItem struct {
	Representations []fixtureRepresentation `json:"representations"`
	Times           int                     `json:"times"`
}

type fixtureCases struct {
	Policy []struct {
		Name     string   `json:"name"`
		MaxBytes int64    `json:"maxBytes"`
		Formats  []string `json:"formats"`
		Code     *string  `json:"code"`
	} `json:"policy"`
	Items []struct {
		Name     string        `json:"name"`
		MaxBytes int64         `json:"maxBytes"`
		Formats  []string      `json:"formats"`
		Items    []fixtureItem `json:"items"`
		Code     *string       `json:"code"`
	} `json:"items"`
}

func (i fixtureItem) build() []clipboard.Item {
	times := i.Times
	if times == 0 {
		times = 1
	}
	var items []clipboard.Item
	for n := 0; n < times; n++ {
		var item clipboard.Item
		item.Representations = []clipboard.Representation{}
		for _, r := range i.Representations {
			data := make([]byte, r.Zeros)
			if r.Text != nil {
				data = []byte(*r.Text)
			}
			item.Representations = append(item.Representations, clipboard.Representation{Format: r.Format, Data: data})
		}
		items = append(items, item)
	}
	return items
}

func expect(t *testing.T, codes map[string]bool, want *string, err error) {
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
	if got := clipboard.Code(err); got != *want {
		t.Fatalf("code %q (%v), want %s", got, err, *want)
	}
}

// TestFixtures runs testdata/clipboard/v1, which the Swift, Java and C# bindings run as well.
func TestFixtures(t *testing.T) {
	var cases fixtureCases
	fixtures.Load(t, "clipboard", 1, &cases)
	codes := fixtures.Codes(t)
	if len(cases.Policy) == 0 || len(cases.Items) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases.Policy {
		t.Run("policy/"+c.Name, func(t *testing.T) {
			expect(t, codes, c.Code, clipboard.ValidatePolicy(clipboard.Policy{MaxBytes: c.MaxBytes, AllowedFormats: c.Formats}))
		})
	}
	for _, c := range cases.Items {
		t.Run("items/"+c.Name, func(t *testing.T) {
			var items []clipboard.Item
			for _, i := range c.Items {
				items = append(items, i.build()...)
			}
			expect(t, codes, c.Code, clipboard.ValidateItems(items, c.MaxBytes, c.Formats))
		})
	}
}

func TestCodeIsEmptyForOtherErrors(t *testing.T) {
	if got := clipboard.Code(errors.New("native failure")); got != "" {
		t.Fatalf("code %q", got)
	}
}
