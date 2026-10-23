package plugintest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
)

const framesDescriptorFixture = "../../../../lib/plugin/testdata/v1/frames-descriptor.json"

// The suite is for guests on a JSON-line transport, which carries frames. The
// reference guest passes it over an in-memory connection.
func TestFramesSuiteOnTheReferenceGuest(t *testing.T) {
	plugintest.RunFrames(t, func(context.Context) (plugin.Backend, error) {
		g, err := plugintest.FramesGuest()
		if err != nil {
			return nil, err
		}
		a, b := net.Pipe()
		go func() { _ = jsonline.ServeGuest(context.Background(), b, g) }()
		return jsonline.NewClient(a), nil
	}, plugintest.FramesOptions{Concurrent: true})
}

// The descriptor in the shared fixture is the one the reference guest serves, so
// guests in other languages can load it from the file.
func TestFramesDescriptorFixture(t *testing.T) {
	want, err := json.Marshal(plugintest.FramesDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if os.Getenv("EXT_UPDATE_FIXTURES") == "1" {
		if err := os.WriteFile(framesDescriptorFixture, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(framesDescriptorFixture)
	if err != nil {
		t.Fatal(err)
	}
	// A checkout may have turned line ends into CRLF; the content is what counts.
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatal("frames-descriptor.json is stale: run EXT_UPDATE_FIXTURES=1 go test -run TestFramesDescriptorFixture ./pkg/plugin-go/contract/plugintest")
	}
	var d plugin.Descriptor
	if err := plugin.Decode(got, &d); err != nil || d.Validate() != nil {
		t.Fatalf("the fixture is not a valid descriptor: %v", err)
	}
}
