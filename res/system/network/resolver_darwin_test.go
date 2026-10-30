//go:build darwin && cgo

package network

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestNativeDNSState(t *testing.T) {
	got := parseNativeDNS("S\t192.0.2.1\nS\tinvalid\nS\t192.0.2.1\nS\t2001:db8::1\nD\texample.test\nD\texample.test\n")
	if !reflect.DeepEqual(got.Servers, []string{"192.0.2.1", "2001:db8::1"}) ||
		!reflect.DeepEqual(got.Search, []string{"example.test"}) {
		t.Fatalf("resolver = %#v", got)
	}
}

func TestNativeResolverCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := platformResolver(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeResolverObservation(t *testing.T) {
	_, state, err := platformResolver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "partial" && state.State != "unavailable" {
		t.Fatalf("state = %#v", state)
	}
}
