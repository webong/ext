package app

import (
	"bytes"
	"strings"
	"testing"

	systemsurface "github.com/webong/ext/res/system/capture/surface"
)

func TestParseDesktopInputShapes(t *testing.T) {
	cases := map[string]struct {
		args  []string
		kind  systemsurface.SurfaceInputKind
		check func(systemsurface.SurfaceInput) bool
	}{
		"text": {
			args: []string{"--text=--value"}, kind: systemsurface.SurfaceInputText,
			check: func(input systemsurface.SurfaceInput) bool { return input.Text == "--value" },
		},
		"key": {
			args: []string{"--key", "t", "--modifier", "command", "--modifier", "shift"},
			kind: systemsurface.SurfaceInputKey,
			check: func(input systemsurface.SurfaceInput) bool {
				return input.Key == "t" && len(input.Modifiers) == 2
			},
		},
		"click": {
			args: []string{"--click", "10,20", "--button", "right"},
			kind: systemsurface.SurfaceInputClick,
			check: func(input systemsurface.SurfaceInput) bool {
				return input.Point == (systemsurface.SurfacePoint{X: 10, Y: 20}) && input.Button == "right"
			},
		},
		"drag": {
			args: []string{"--drag", "1,2,3,4"}, kind: systemsurface.SurfaceInputDrag,
			check: func(input systemsurface.SurfaceInput) bool {
				return input.Point == (systemsurface.SurfacePoint{X: 1, Y: 2}) &&
					input.End == (systemsurface.SurfacePoint{X: 3, Y: 4})
			},
		},
		"scroll": {
			args: []string{"--scroll", "5,6,-120,240"}, kind: systemsurface.SurfaceInputScroll,
			check: func(input systemsurface.SurfaceInput) bool {
				return input.Point == (systemsurface.SurfacePoint{X: 5, Y: 6}) &&
					input.DeltaX == -120 && input.DeltaY == 240
			},
		},
	}
	for name, test := range cases {
		request, err := parseDesktopArgs(append([]string{"--window", "window:opaque"}, test.args...))
		if err != nil {
			t.Errorf("%s = %v", name, err)
			continue
		}
		if !request.hasInput || request.input.Kind != test.kind || !test.check(request.input) {
			t.Errorf("%s input = %#v", name, request.input)
		}
	}
}

func TestParseDesktopRejectsAmbiguousInput(t *testing.T) {
	cases := map[string][]string{
		"twoInputs":     {"--window", "window:opaque", "--text", "a", "--key", "b"},
		"modifierNoKey": {"--window", "window:opaque", "--text", "a", "--modifier", "shift"},
		"buttonOnText":  {"--window", "window:opaque", "--text", "a", "--button", "right"},
		"outWithInput":  {"--window", "window:opaque", "--text", "a", "--out", "file"},
		"badPoint":      {"--window", "window:opaque", "--click", "nope"},
		"badDrag":       {"--window", "window:opaque", "--drag", "1,2,3"},
		"badScroll":     {"--window", "window:opaque", "--scroll", "1,2,3"},
		"unknownFlag":   {"--window", "window:opaque", "--nope"},
		"jsonWithValue": {"--window", "window:opaque", "--json=yes"},
		"missingValue":  {"--window"},
	}
	for name, args := range cases {
		if _, err := parseDesktopArgs(args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestDesktopCommandRequiresObservedWindow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := desktopCommand([]string{"capture", "--window", "window:missing", "--out", "-"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("missing window was accepted: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not currently observed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if code = desktopCommand([]string{"input", "--window", "window:missing", "--text", "hello"}, &stdout, &stderr); code == 0 {
		t.Fatal("missing window input was accepted")
	}
	if code = desktopCommand([]string{"bogus"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown action = %d", code)
	}
	if code = desktopCommand([]string{"capture", "--out", "-"}, &stdout, &stderr); code != 2 {
		t.Fatalf("capture without window = %d", code)
	}
}
