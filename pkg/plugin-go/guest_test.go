//go:build ext_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/inprocess"
	"github.com/webong/ext/pkg/plugin/plugintest"
	"reflect"
	"testing"
	"time"
)

func TestSharedGuestConformance(t *testing.T) {
	plugintest.Run(t, func(context.Context) (plugin.Backend, error) {
		ref, err := plugintest.Guest()
		if err != nil {
			return nil, err
		}
		g, err := NewGuest(plugintest.Descriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
			resp, err := ref.Invoke(ctx, r)
			if err != nil {
				return nil, err
			}
			if resp.Error != nil {
				return nil, resp.Error
			}
			return resp.Payload, nil
		}})
		if err != nil {
			return nil, err
		}
		t.Cleanup(g.Destroy)
		return inprocess.New(g)
	})
}
func TestSharedGuestDifferential(t *testing.T) {
	type key struct{}
	for _, mode := range []string{"echo", "private", "public", "empty", "panic", "values"} {
		t.Run(mode, func(t *testing.T) {
			handler := func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
				switch mode {
				case "private":
					return nil, errors.New("secret")
				case "public":
					return nil, &plugin.RemoteError{Code: "busy", Message: "later", RetryAfterMilliseconds: 10}
				case "empty":
					return nil, nil
				case "panic":
					panic("secret")
				case "values":
					if ctx.Value(key{}) != "value" {
						return nil, errors.New("lost context")
					}
				}
				return r.Payload, nil
			}
			g, err := NewGuest(plugintest.Descriptor(), plugin.GuestOptions{Handler: handler})
			if err != nil {
				t.Fatal(err)
			}
			defer g.Destroy()
			ref, _ := plugin.NewGuest(plugintest.Descriptor(), plugin.GuestOptions{Handler: handler})
			r := plugin.Request{APIVersion: plugin.APIVersion, ID: "1", Plugin: plugintest.Descriptor().Identity, Contract: plugintest.Descriptor().Contracts[0].ContractRef, Operation: "echo", Deadline: time.Now().Add(time.Second), Payload: json.RawMessage(`{"n":9007199254740993}`)}
			ctx := context.WithValue(context.Background(), key{}, "value")
			got, err := g.Invoke(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "panic" {
				if got.Error == nil || got.Error.Code != "operation_failed" {
					t.Fatal(got)
				}
				return
			}
			want, e := ref.Invoke(ctx, r)
			if e != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v %v; want %#v %v", got, err, want, e)
			}
			r.Operation = "unknown"
			got, err = g.Invoke(ctx, r)
			want, e = ref.Invoke(ctx, r)
			if err != nil || e != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("mismatch got %#v, want %#v", got, want)
			}
		})
	}
}
