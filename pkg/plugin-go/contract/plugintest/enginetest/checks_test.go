package enginetest

import "testing"

func TestChecksAreNamedOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Checks() {
		if c.Name == "" || c.Run == nil || seen[c.Name] {
			t.Errorf("check %q is unnamed, has no body or repeats a name", c.Name)
		}
		seen[c.Name] = true
	}
	if len(seen) != 19 {
		t.Errorf("%d checks, want 19", len(seen))
	}
}
