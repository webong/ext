package extension

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProgressRoundTrip(t *testing.T) {
	in := InstallResult{Status: "awaiting-browser-action", Browser: "example", ID: "abc", Source: "/tmp/x", NextAction: "Do <this> & that"}
	line, err := ProgressLine(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(line, "\r\n") || !strings.HasPrefix(line, ProgressPrefix) {
		t.Fatalf("bad line %q", line)
	}
	for _, withNewline := range []string{line, line + "\n", line + "\r\n"} {
		out, ok := ParseProgress(withNewline)
		if !ok || out.Status != in.Status || out.ID != in.ID || out.NextAction != in.NextAction {
			t.Fatalf("parse %q: %+v %v", withNewline, out, ok)
		}
	}
}

func TestParseProgressIgnoresOtherLines(t *testing.T) {
	for _, line := range []string{
		"", "launching browser", "browser management progress:", ProgressPrefix, ProgressPrefix + "{", ProgressPrefix + "[]",
		ProgressPrefix + `{"browser":"x"}`, // no status
		" " + ProgressPrefix + `{"status":"installed"}`,
		ProgressPrefix + `{"status":"` + strings.Repeat("a", MaxProgressLineBytes) + `"}`,
	} {
		if _, ok := ParseProgress(line); ok {
			t.Errorf("accepted %.60q", line)
		}
	}
}

func TestAdapterProgressMatchesProtocol(t *testing.T) {
	// The prefix is a wire constant; changing it breaks hosts.
	if ProgressPrefix != "browser management progress: " {
		t.Fatal("progress prefix changed")
	}
	var buffer bytes.Buffer
	line, _ := ProgressLine(InstallResult{Status: "installed"})
	buffer.WriteString(line)
	var payload InstallResult
	if err := json.Unmarshal(buffer.Bytes()[len(ProgressPrefix):], &payload); err != nil || payload.Status != "installed" {
		t.Fatalf("%v %+v", err, payload)
	}
}
