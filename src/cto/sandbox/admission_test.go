package sandbox

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestAdmissionReportLeavesGuestOutputOnTheStream(t *testing.T) {
	enforced, err := Required(Policy{})
	if err != nil {
		t.Fatal(err)
	}
	sent := Report{Boundary: Runtime, Driver: "fixture", Enforced: enforced}
	var wire bytes.Buffer
	if err := AwaitAdmission(strings.NewReader(AdmissionApproval), &wire, sent); err != nil {
		t.Fatal(err)
	}
	wire.WriteString("guest output")
	reader := bufio.NewReader(&wire)
	got, err := ReadAdmissionReport(reader)
	if err != nil || !Sandboxed(got) || got.Driver != sent.Driver {
		t.Fatalf("report = %+v, %v", got, err)
	}
	output, err := io.ReadAll(reader)
	if err != nil || string(output) != "guest output" {
		t.Fatalf("output = %q, %v", output, err)
	}
}

func TestReadAdmissionReportRejectsInvalidFrames(t *testing.T) {
	for _, tt := range []struct {
		name, line string
		want       error
	}{
		{"missing", "", io.EOF},
		{"partial", "{", ErrInvalid},
		{"oversized", strings.Repeat(" ", maxAdmissionReportBytes) + "\n", ErrInvalid},
		{"missing grant", `{"boundary":"runtime","enforced":[]}` + "\n", ErrInvalid},
		{"null grant", `{"boundary":"runtime","grant":null,"enforced":[]}` + "\n", ErrInvalid},
		{"trailing JSON", `{"boundary":"runtime","grant":{},"enforced":[]} {}` + "\n", ErrInvalid},
		{"contradictory grant", `{"boundary":"runtime","grant":{"process":{"children":true,"Children":false}},"enforced":["filesystem","network.none","process","devices","environment","lifecycle.fresh"]}` + "\n", ErrInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ReadAdmissionReport(bufio.NewReader(strings.NewReader(tt.line))); !errors.Is(err, tt.want) {
				t.Fatalf("ReadAdmissionReport = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAwaitAdmissionRequiresExplicitApproval(t *testing.T) {
	enforced, err := Required(Policy{})
	if err != nil {
		t.Fatal(err)
	}
	report := Report{Boundary: Runtime, Enforced: enforced}
	for _, response := range []string{"", "run", "no!\n"} {
		if err := AwaitAdmission(strings.NewReader(response), io.Discard, report); !errors.Is(err, ErrDenied) {
			t.Errorf("response %q: %v, want ErrDenied", response, err)
		}
	}
}
