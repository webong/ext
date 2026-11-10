package host

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/webong/ext/res/system/internal/fixtures"
	proc "github.com/webong/ext/res/system/service/process"
)

type hostCases struct {
	Cases []struct {
		Name     string          `json:"name"`
		Snapshot json.RawMessage `json:"snapshot"`
		Code     *string         `json:"code"`
	} `json:"cases"`
}

type capabilityCases struct {
	Cases []struct {
		State  string  `json:"state"`
		Reason *string `json:"reason"`
		Code   *string `json:"code"`
	} `json:"cases"`
}

// decode reads a snapshot as a consumer would and validates it; a snapshot that does not decode is
// invalid too.
func decode(data []byte) error {
	var info ResourceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return info.Validate()
}

// TestFixtures runs testdata/host/v1 and testdata/capability/v1, which the Swift and Java bindings
// run as well.
func TestFixtures(t *testing.T) {
	var cases hostCases
	fixtures.Load(t, "host", 1, &cases)
	codes := fixtures.Codes(t)
	if len(cases.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases.Cases {
		t.Run("snapshots/"+c.Name, func(t *testing.T) {
			err := decode(c.Snapshot)
			if c.Code == nil {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if !codes[*c.Code] {
				t.Fatalf("code %q is not in testdata/codes.json", *c.Code)
			}
			if err == nil {
				t.Fatalf("accepted, want %s", *c.Code)
			}
			if got := Code(err); got != *c.Code {
				t.Fatalf("code %q (%v), want %s", got, err, *c.Code)
			}
		})
	}
	var capability capabilityCases
	fixtures.Load(t, "capability", 1, &capability)
	for _, c := range capability.Cases {
		name, reason := c.State, ""
		if c.Reason != nil {
			reason = *c.Reason
			name += "+" + reason
		}
		t.Run("capability/"+name, func(t *testing.T) {
			err := proc.CollectionStatus{State: c.State, Reason: reason}.Validate()
			if (err != nil) != (c.Code != nil) {
				t.Fatalf("%v, want code %v", err, c.Code)
			}
		})
	}
}

// TestWireRoundTrip keeps the wire form: uptime is uptime_seconds, a number.
func TestWireRoundTrip(t *testing.T) {
	var cases hostCases
	fixtures.Load(t, "host", 1, &cases)
	var info ResourceInfo
	if err := json.Unmarshal(cases.Cases[0].Snapshot, &info); err != nil || info.Uptime == nil || info.Uptime.Seconds() != 3600.5 {
		t.Fatalf("%+v, %v", info, err)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatal(err)
	}
	if generic["uptime_seconds"] != 3600.5 || generic["uptime"] != nil {
		t.Fatalf("wire form %s", encoded)
	}
	if err := decode(encoded); err != nil {
		t.Fatalf("round trip: %v", err)
	}
}

// TestObserverProducesValidSnapshots checks the rules against what this machine reports.
func TestObserverProducesValidSnapshots(t *testing.T) {
	info, err := DiscoverResources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := info.Validate(); err != nil {
		t.Fatalf("%v\n%+v", err, info)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(encoded); err != nil {
		t.Fatalf("round trip: %v\n%s", err, encoded)
	}
}
