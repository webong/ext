package engine

import (
	"strings"
	"testing"
)

func TestSplitProgressText(t *testing.T) {
	const max = MaxProgressTextBytes
	long := strings.Repeat("a", max-1) + "€" + strings.Repeat("b", 10) // the euro sign straddles the bound
	cases := map[string]string{
		"empty":                  "",
		"short":                  "hello",
		"exactly the bound":      strings.Repeat("a", max),
		"one over":               strings.Repeat("a", max+1),
		"multibyte at the bound": long,
		"many pieces":            strings.Repeat("🙂", 3000),
		"invalid utf-8":          "ok\xff\xfe" + strings.Repeat("c", max),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			pieces := SplitProgressText(text)
			if text == "" {
				if pieces != nil {
					t.Fatalf("empty text must give no pieces, got %d", len(pieces))
				}
				return
			}
			var joined strings.Builder
			for i, piece := range pieces {
				if len(piece) == 0 || len(piece) > max {
					t.Fatalf("piece %d has %d bytes", i, len(piece))
				}
				if err := (Progress{Kind: "log", Text: piece}).Validate(); err != nil {
					t.Fatalf("piece %d is not a legal event: %v", i, err)
				}
				joined.WriteString(piece)
			}
			want := strings.ToValidUTF8(text, "�")
			if joined.String() != want {
				t.Fatal("the pieces do not rebuild the text")
			}
		})
	}
	if got := SplitProgressText("hello"); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("short text is one piece, got %q", got)
	}
}
