package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestSharedGuestValidationAndDeadlines(t *testing.T) {
	d := testDescriptor()
	calls := 0
	guest, err := NewGuest(d, GuestOptions{Handler: func(context.Context, Request) (json.RawMessage, error) {
		calls++
		return nil, errors.New("private internals")
	}})
	if err != nil {
		t.Fatal(err)
	}
	d.Contracts[0].Operations[0].Name = "tampered"
	actual, err := guest.Handshake(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := MatchHandshake(testDescriptor(), actual); err != nil {
		t.Fatal(err)
	}
	r := Request{APIVersion: APIVersion, ID: "1", Plugin: actual.Identity, Contract: actual.Contracts[0].ContractRef, Operation: "inspect", Surface: "observation", Deadline: time.Now().Add(time.Second)}
	bad := r
	bad.Surface = "action"
	response, err := guest.Invoke(context.Background(), bad)
	if err != nil || response.Error == nil || response.Error.Code != "invalid_request" || calls != 0 {
		t.Fatalf("invalid request dispatched: %+v %v", response, err)
	}
	expired := r
	expired.Deadline = time.Now().Add(-time.Second)
	response, err = guest.Invoke(context.Background(), expired)
	if err != nil || response.Error == nil || calls != 0 {
		t.Fatalf("expired request dispatched: %+v %v", response, err)
	}
	response, err = guest.Invoke(context.Background(), r)
	if err != nil || calls != 1 || response.Error.Message != "plugin operation failed" {
		t.Fatalf("guest response: %+v %v", response, err)
	}
	if _, err := NewGuest(actual, GuestOptions{}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}
