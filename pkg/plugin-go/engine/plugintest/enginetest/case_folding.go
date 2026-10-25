package enginetest

import (
	"encoding/json"
	"testing"
	"unicode"

	packagekit "github.com/webong/ext/pkg/plugin-go/engine/package"
)

// CaseFoldingMatchesGoForEveryRune compares how the engine and Go decide that two artifact paths
// differ only in case, for every character whose lowercase differs from itself and for those next
// to them that do not fold. The engine pins the Unicode simple lowercase mapping of one version
// (lib/plugin/src/unicode_lower.h), where Go follows its toolchain: this test is what makes a
// toolchain that moves ahead of the table visible, and is skipped, with the reason, until the table
// is regenerated for it (pkg/plugin-go/cgoengine/tools/README.md).
func CaseFoldingMatchesGoForEveryRune(t *testing.T, e Engine) {
	const pinned = "17.0.0"
	if unicode.Version != pinned {
		t.Skipf("this toolchain has Unicode %s and the engine pins %s: regenerate the table, or decide to keep the older mapping", unicode.Version, pinned)
	}
	doc, _ := manifestDocument(t)
	check := func(first, second string) (want, got error) {
		one := deepCopy(doc).(map[string]any)
		list := one["artifacts"].([]any)
		a, b := list[0].(map[string]any), list[1].(map[string]any)
		a["path"], b["path"] = first, second
		delete(b, "kind")
		raw, err := json.Marshal(one)
		if err != nil {
			t.Fatal(err)
		}
		_, want = packagekit.Decode(raw)
		_, got = e.Call("package.validate", raw)
		return want, got
	}
	var folded, informative, diverged int
	for r := rune(0x21); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		lower := unicode.ToLower(r)
		// A character that folds against its lowercase, or one that does not against its neighbour.
		other := lower
		if lower == r {
			other = r + 1
			if other >= 0xD800 && other <= 0xDFFF {
				continue
			}
		} else {
			folded++
		}
		if lower == r && r%97 != 0 {
			continue // sample the characters that do not fold
		}
		want, got := check("a/"+string(r), "a/"+string(other))
		if want != nil {
			informative++
		}
		if (want == nil) != (got == nil) {
			diverged++
			if diverged <= 8 {
				t.Errorf("U+%04X and U+%04X: Go=%s C=%s", r, other, errText(want), errText(got))
			}
		}
	}
	t.Logf("%d characters fold; %d pairs rejected as duplicates by Go; %d divergences", folded, informative, diverged)
	if folded < 1000 || informative < 100 {
		t.Fatalf("the comparison proves little: %d fold, %d pairs rejected", folded, informative)
	}
}
