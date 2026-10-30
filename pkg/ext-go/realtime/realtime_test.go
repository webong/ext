package realtime_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
	bridge "github.com/webong/ext/pkg/ext-go/realtime"
	domain "github.com/webong/ext/res/int/realtime"
)

func TestLanguageNeutralFixtures(t *testing.T) {
	requestJSON, err := os.ReadFile("testdata/v1/open-request.json")
	if err != nil { t.Fatal(err) }
	var request domain.OpenRequest
	if err := json.Unmarshal(requestJSON, &request); err != nil { t.Fatal(err) }
	if err := request.Validate(); err != nil { t.Fatal(err) }
	eventJSON, err := os.ReadFile("testdata/v1/event.json")
	if err != nil { t.Fatal(err) }
	var event domain.Event
	if err := json.Unmarshal(eventJSON, &event); err != nil { t.Fatal(err) }
	if err := event.Validate(); err != nil { t.Fatal(err) }
}

type fixtureProvider struct{ session *fixtureSession }

func (p *fixtureProvider) Open(context.Context, domain.OpenRequest) (domain.Session, error) {
	p.session = &fixtureSession{}
	return p.session, nil
}
func (*fixtureProvider) Close() error { return nil }

type fixtureSession struct {
	text   string
	closed bool
}

func (s *fixtureSession) SendText(_ context.Context, value string) error     { s.text = value; return nil }
func (*fixtureSession) SendAudio(context.Context, []byte) error              { return nil }
func (*fixtureSession) CommitAudio(context.Context) error                    { return nil }
func (*fixtureSession) Respond(context.Context) error                        { return nil }
func (*fixtureSession) Interrupt(context.Context) error                      { return nil }
func (*fixtureSession) SendToolResult(context.Context, string, []byte) error { return nil }
func (s *fixtureSession) Receive(context.Context) (domain.Event, error) {
	return domain.Event{Kind: "text.delta", Text: s.text, OccurredAt: time.Now()}, nil
}
func (s *fixtureSession) Close() error { s.closed = true; return nil }

func TestRealtimeHostGuestSession(t *testing.T) {
	ctx := context.Background()
	authorize := func(context.Context, plugin.Request) error { return nil }
	registry, err := author.New(plugin.Identity{ID: "realtime", Revision: "r1", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	provider := &fixtureProvider{}
	descriptor, err := bridge.RegisterProvider(registry, provider, authorize)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := registry.Guest(author.Options{Authorize: authorize})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := inprocess.New(guest)
	if err != nil {
		t.Fatal(err)
	}
	pluginSession, err := plugin.Open(ctx, *descriptor, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Authorize: authorize,
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return backend, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pluginSession.Close(ctx)
	client := bridge.NewClient(pluginSession)
	session, err := client.Open(ctx, domain.OpenRequest{APIVersion: domain.APIVersion, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Open(ctx, domain.OpenRequest{APIVersion: domain.APIVersion, Model: "m"}); err == nil {
		t.Fatal("second live session accepted")
	}
	if err := session.SendText(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := session.Respond(ctx); err != nil {
		t.Fatal(err)
	}
	event, err := session.Receive(ctx)
	if err != nil || event.Text != "hello" {
		t.Fatalf("event: %+v, %v", event, err)
	}
	if err := session.CloseContext(ctx); err != nil {
		t.Fatal(err)
	}
	if !provider.session.closed {
		t.Fatal("provider session was not closed")
	}
}
