package inference_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/guest/stream"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
	bridge "github.com/webong/ext/src/ctn/inference"
	domain "github.com/webong/ext/res/int/inference"
)

// audio holds bytes that are not UTF-8, so a chunk that crossed the wire unencoded would fail.
var audio = []byte{0xff, 0xfe, 0x00, 0x80, 0xc3, 0x28, 'R', 'I', 'F', 'F'}

type streaming struct{ ownCompleted bool }

func (streaming) Info(context.Context) (domain.ProviderInfo, error) {
	return domain.ProviderInfo{ID: "test", Name: "Test", Version: "1"}, nil
}
func (streaming) Close() error { return nil }

func (p streaming) Stream(ctx context.Context, _ domain.GenerationRequest, sink domain.EventSink) (domain.GenerationResponse, error) {
	resp := domain.GenerationResponse{
		Model: "m",
		Message: domain.Message{
			Role:      domain.RoleAssistant,
			Parts:     []domain.Part{{Kind: domain.PartKindText, Text: "hello"}},
			ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "lookup", Arguments: []byte(`{"q":"x"}`)}},
		},
		StopReason: domain.FinishReasonToolCalls,
		Usage:      domain.TokenUsage{Input: 3, Output: 5, Total: 8},
	}
	if err := sink.Emit(ctx, domain.GenerationEvent{Kind: domain.EventKindTextDelta, Text: "hello"}); err != nil {
		return domain.GenerationResponse{}, err
	}
	if p.ownCompleted {
		if err := sink.Emit(ctx, domain.GenerationEvent{Kind: domain.EventKindCompleted, Response: &resp}); err != nil {
			return domain.GenerationResponse{}, err
		}
	}
	return resp, nil
}

func (streaming) Synthesize(context.Context, domain.SpeechRequest) (domain.SpeechStream, error) {
	return io.NopCloser(bytes.NewReader(audio)), nil
}

func open(t *testing.T, p domain.Provider) *bridge.Client {
	t.Helper()
	ctx := context.Background()
	auth := func(context.Context, plugin.Request) error { return nil }
	r, err := author.New(plugin.Identity{ID: "test", Revision: "r1", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := bridge.RegisterProvider(r, p, auth)
	if err != nil {
		t.Fatal(err)
	}
	g, err := r.Guest(author.Options{Authorize: auth})
	if err != nil {
		t.Fatal(err)
	}
	b, err := inprocess.New(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := plugin.Open(ctx, *d, plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }, Authorize: auth})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(ctx) })
	return bridge.NewClient(s)
}

func drain(t *testing.T, c *stream.Client) [][]byte {
	t.Helper()
	ctx := context.Background()
	var items [][]byte
	for {
		batch, err := c.Read(ctx, 16)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range batch.Items {
			items = append(items, item)
		}
		if batch.Done {
			_ = c.Close(ctx)
			return items
		}
	}
}

func TestStreamCarriesFinalResponse(t *testing.T) {
	for _, own := range []bool{false, true} {
		c := open(t, streaming{ownCompleted: own})
		s, err := c.Stream(context.Background(), domain.GenerationRequest{})
		if err != nil {
			t.Fatal(err)
		}
		items := drain(t, s)
		if len(items) != 2 {
			t.Fatalf("own completed %v: got %d events, want a delta and one completed", own, len(items))
		}
		last, err := bridge.DecodeEvent(items[1])
		if err != nil {
			t.Fatal(err)
		}
		if last.Kind != domain.EventKindCompleted || last.Response == nil {
			t.Fatalf("own completed %v: last event %+v, want completed with a response", own, last)
		}
		r := last.Response
		if r.StopReason != domain.FinishReasonToolCalls || len(r.Message.ToolCalls) != 1 || r.Message.ToolCalls[0].Name != "lookup" || r.Usage.Output != 5 {
			t.Fatalf("own completed %v: response lost fields: %+v", own, r)
		}
	}
}

func TestSynthesizeCarriesBinaryAudio(t *testing.T) {
	c := open(t, streaming{})
	s, err := c.Synthesize(context.Background(), domain.SpeechRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for _, item := range drain(t, s) {
		chunk, err := bridge.DecodeAudio(item)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, chunk...)
	}
	if !bytes.Equal(got, audio) {
		t.Fatalf("audio %x, want %x", got, audio)
	}
}
