package cgoengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest/enginetest"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
)

// The tests that compare the C engine with the pure-Go session on progress
// frames run one guest through both. The guest is this test binary started
// again with guestEnv set: "go" serves the shared Go guest, and "raw:<mode>"
// speaks the wire by hand so it can break the rules the Go guest cannot.

const guestEnv = "CENGINE_PROGRESS_GUEST"

var progressRef = plugin.ContractRef{Name: "progress.test", Version: "v1"}

func progressDescriptor() plugin.Descriptor {
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "cgoengine/progress", Revision: "r1"},
		Contracts: []plugin.Contract{{ContractRef: progressRef, Operations: []plugin.Operation{
			{Name: "plain"}, {Name: "stream", Progress: true}, {Name: "flood", Progress: true},
			{Name: "fail", Progress: true}, {Name: "limits", Progress: true}, {Name: "plainlimits"},
			{Name: "flooddrops", Progress: true},
		}}},
	}
}

func TestMain(m *testing.M) {
	enginetest.MaybeServeGuest()
	if mode := os.Getenv(guestEnv); mode != "" {
		os.Exit(runProgressGuest(mode))
	}
	os.Exit(m.Run())
}

func runProgressGuest(mode string) int {
	if strings.HasPrefix(mode, "rev-") {
		return runReverseGuest(mode)
	}
	if strings.HasPrefix(mode, "raw:") {
		return runRawGuest(strings.TrimPrefix(mode, "raw:"))
	}
	guest, err := newProgressGuest()
	if err != nil {
		return 2
	}
	if err := jsonline.ServeStdio(context.Background(), guest); err != nil {
		return 1
	}
	return 0
}

// newProgressGuest is the Go guest that reports progress through
// plugin.ProgressFrom. It serves a child process or the host's own.
func newProgressGuest() (*plugin.Guest, error) {
	return plugin.NewGuest(progressDescriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		report := plugin.ProgressFrom(ctx)
		done := json.RawMessage(`{"done":true}`)
		switch r.Operation {
		case "stream":
			_ = report.Report(plugin.Progress{Kind: "progress", Completed: 1, Total: 3})
			_ = report.Report(plugin.Progress{Kind: "log", Stream: "out", Text: "hello"})
			_ = report.Report(plugin.Progress{Kind: "log", Stream: "err", Text: "oops"})
		case "flood":
			for i := 0; i < 300; i++ {
				_ = report.Report(plugin.Progress{Kind: "log", Text: "x"})
			}
		case "flooddrops":
			// What the plugin is told about its own events: a dropped one is an error.
			drops := 0
			for i := 0; i < 300; i++ {
				if errors.Is(report.Report(plugin.Progress{Kind: "log", Text: "x"}), plugin.ErrProgressDropped) {
					drops++
				}
			}
			return json.Marshal(map[string]int{"drops": drops})
		case "fail":
			_ = report.Report(plugin.Progress{Kind: "log", Text: "before"})
			return nil, &plugin.RemoteError{Code: "busy", Message: "try later"}
		case "limits", "plainlimits":
			l, ok := plugin.ProgressLimitsFrom(ctx)
			return json.Marshal(map[string]any{"declared": ok, "events": l.EventsPerSecond, "bytes": l.BytesPerSecond})
		}
		return done, nil
	}})
}

// runRawGuest answers the handshake, then answers every call with the frames
// its mode names, written by hand.
func runRawGuest(mode string) int {
	in := bufio.NewReader(os.Stdin)
	line, err := in.ReadString('\n')
	if err != nil {
		return 0
	}
	descriptor, _ := json.Marshal(progressDescriptor())
	frame := func(parent, fields string) string {
		return fmt.Sprintf(`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":%q,%s}`+"\n", parent, fields)
	}
	if mode == "hellofirst" {
		fmt.Print(frame("hello", `"kind":"log","text":"early"`))
	}
	fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":"hello","payload":%s}`+"\n", descriptor)
	for {
		if line, err = in.ReadString('\n'); err != nil {
			return 0
		}
		var request struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(line), &request) != nil {
			return 1
		}
		id := request.ID
		response := fmt.Sprintf(`{"apiVersion":"ext.plugin/v1","id":%q,"payload":{"done":true}}`+"\n", id)
		switch mode {
		case "undeclared", "ok":
			fmt.Print(frame(id, `"kind":"log","text":"x"`) + response)
		case "wrongparent":
			fmt.Print(frame("999", `"kind":"log","text":"x"`) + response)
		case "badkind":
			fmt.Print(frame(id, `"kind":"debug"`) + response)
		case "streamprogress":
			fmt.Print(frame(id, `"kind":"progress","stream":"out"`) + response)
		case "overtotal":
			fmt.Print(frame(id, `"kind":"progress","completed":5,"total":2`) + response)
		case "negative":
			fmt.Print(frame(id, `"kind":"progress","completed":-1`) + response)
		case "fraction":
			fmt.Print(frame(id, `"kind":"progress","completed":1.5`) + response)
		case "bigtext":
			fmt.Print(frame(id, `"kind":"log","text":"`+strings.Repeat("x", 2049)+`"`) + response)
		case "dupkey":
			fmt.Print(frame(id, `"kind":"log","kind":"log"`) + response)
		case "unknownfield":
			fmt.Print(frame(id, `"kind":"log","extra":1`) + response)
		case "unknown":
			fmt.Print(`{"frame":"mystery"}` + "\n" + response)
		case "notobject":
			fmt.Print("[1,2]\n" + response)
		case "reverseresponse":
			fmt.Print(`{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":null}` + "\n" + response)
		case "wrongversion":
			fmt.Printf(`{"apiVersion":"ext.plugin/v2","id":%q,"payload":null}`+"\n", id)
		case "extrafield":
			fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":%q,"payload":null,"extra":1}`+"\n", id)
		case "dupresponse":
			fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":%q,"id":%q,"payload":null}`+"\n", id, id)
		case "wrongid":
			fmt.Print(`{"apiVersion":"ext.plugin/v1","id":"other","payload":null}` + "\n")
		case "nullroot":
			fmt.Print("null\n")
		case "garbage":
			fmt.Print("not json\n")
		case "after":
			fmt.Print(response + frame(id, `"kind":"log","text":"late"`))
		default:
			return 2
		}
	}
}
