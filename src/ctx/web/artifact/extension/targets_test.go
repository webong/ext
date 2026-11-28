package extension

import "testing"

func TestResolveTargetKeepsAdapterAndProfileBinding(t *testing.T) {
	target := NewTarget("custom_provider", "/native/browser", "/profiles/work", "", "Work label")
	target.Profile = "work"
	for _, test := range []struct {
		name, browser, profile, id string
		wantError                  bool
	}{
		{name: "profile selector", browser: "custom_provider", profile: "work"},
		{name: "explicit id", browser: "custom_provider", profile: "work", id: target.ID},
		{name: "wrong profile", browser: "custom_provider", profile: "personal", id: target.ID, wantError: true},
		{name: "wrong adapter", browser: "another_provider", profile: "work", id: target.ID, wantError: true},
		{name: "stale id", browser: "custom_provider", profile: "work", id: "gone", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := ResolveTarget([]BrowserTarget{target}, test.browser, test.profile, test.id)
			if (err != nil) != test.wantError || (err == nil && resolved.ID != target.ID) {
				t.Fatalf("target=%+v err=%v", resolved, err)
			}
		})
	}
}

func TestResolveTargetRequiresIDWhenProfileMatchesSeveralInstallations(t *testing.T) {
	first := NewTarget("custom_provider", "/native/stable", "/profiles/work", "", "Work")
	first.Profile = "work"
	second := NewTarget("custom_provider", "/native/preview", "/profiles/work", "", "Work")
	second.Profile = "work"
	if _, err := ResolveTarget([]BrowserTarget{first, second}, "custom_provider", "work", ""); err == nil {
		t.Fatal("ambiguous installation was selected without a target ID")
	}
}
