package surface

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/webong/ext/src/ctx/system/internal/fixtures"
)

type snapshotCase struct {
	Name     string          `json:"name"`
	Snapshot json.RawMessage `json:"snapshot"`
	Code     *string         `json:"code"`
}

type surfaceCases struct {
	WindowsControl struct {
		PID            int    `json:"pid"`
		ProcessStartID string `json:"processStartId"`
		Handle         string `json:"handle"`
		WindowID       string `json:"windowId"`
	} `json:"windowsControl"`
	Surface        []snapshotCase `json:"surface"`
	Permission     []snapshotCase `json:"permission"`
	Accessibility  []snapshotCase `json:"accessibility"`
	SurfaceOptions []struct {
		Name        string  `json:"name"`
		MaxDisplays int     `json:"maxDisplays"`
		MaxWindows  int     `json:"maxWindows"`
		Code        *string `json:"code"`
	} `json:"surfaceOptions"`
	AccessibilityOptions []struct {
		Name        string  `json:"name"`
		MaxElements int     `json:"maxElements"`
		MaxDepth    int     `json:"maxDepth"`
		Code        *string `json:"code"`
	} `json:"accessibilityOptions"`
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
	if got := Code(err); got != *want {
		t.Fatalf("code %q (%v), want %s", got, err, *want)
	}
}

// read decodes a snapshot as a consumer would and validates it; a snapshot that does not decode is
// invalid too.
func read[T interface{ Validate() error }](data []byte) error {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return value.Validate()
}

// TestFixtures runs testdata/surface/v1, which the Swift and Java bindings run as well.
func TestFixtures(t *testing.T) {
	var cases surfaceCases
	fixtures.Load(t, "surface", 1, &cases)
	codes := fixtures.Codes(t)
	windows := cases.WindowsControl
	if got := surfaceWindowID(windows.PID, windows.ProcessStartID, windows.Handle); got != windows.WindowID {
		t.Errorf("Windows window identity %q, want %q", got, windows.WindowID)
	}
	if len(cases.Surface) == 0 || len(cases.Permission) == 0 || len(cases.Accessibility) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases.Surface {
		t.Run("surface/"+c.Name, func(t *testing.T) { expectCode(t, codes, c.Code, read[SurfaceSnapshot](c.Snapshot)) })
	}
	for _, c := range cases.Permission {
		t.Run("permission/"+c.Name, func(t *testing.T) { expectCode(t, codes, c.Code, read[PermissionSnapshot](c.Snapshot)) })
	}
	for _, c := range cases.Accessibility {
		t.Run("accessibility/"+c.Name, func(t *testing.T) { expectCode(t, codes, c.Code, read[AccessibilitySnapshot](c.Snapshot)) })
	}
	for _, c := range cases.SurfaceOptions {
		t.Run("surfaceOptions/"+c.Name, func(t *testing.T) {
			expectCode(t, codes, c.Code, SurfaceOptions{MaxDisplays: c.MaxDisplays, MaxWindows: c.MaxWindows, Timeout: time.Second}.Validate())
		})
	}
	for _, c := range cases.AccessibilityOptions {
		t.Run("accessibilityOptions/"+c.Name, func(t *testing.T) {
			expectCode(t, codes, c.Code, AccessibilityOptions{MaxElements: c.MaxElements, MaxDepth: c.MaxDepth, Timeout: time.Second}.Validate())
		})
	}
}
