package adapterkit

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type testStore struct {
	value []byte
	item  string
}

func (store *testStore) Check(context.Context) error { return nil }
func (store *testStore) Get(_ context.Context, item string) ([]byte, error) {
	store.item = item
	if store.value == nil {
		return nil, errors.New("missing")
	}
	return append([]byte(nil), store.value...), nil
}
func (store *testStore) Put(_ context.Context, item string, value []byte, replace bool) error {
	store.item = item
	if store.value != nil && !replace {
		return ErrExists
	}
	store.value = append([]byte(nil), value...)
	return nil
}

func TestCredentialProtocolRoundTrip(t *testing.T) {
	store := &testStore{}
	var out, errout bytes.Buffer
	if code := Run(Invocation{Operation: "share", Arguments: []string{"credential", "put", "service=ctx&account=work"}}, strings.NewReader("private-value"), &out, &errout, store); code != 0 {
		t.Fatalf("put code=%d diagnostics=%q", code, errout.String())
	}
	if out.Len() != 0 || store.item != "service=ctx&account=work" {
		t.Fatalf("put emitted data or changed item: output=%q item=%q", out.String(), store.item)
	}
	if code := Run(Invocation{Operation: "share", Arguments: []string{"credential", "put", store.item}}, strings.NewReader("second-value"), &out, &errout, store); code == 0 {
		t.Fatal("put replaced an item without --replace")
	}
	if strings.Contains(errout.String(), "private-value") || strings.Contains(errout.String(), "second-value") {
		t.Fatal("secret appeared in diagnostics")
	}
	out.Reset()
	errout.Reset()
	if code := Run(Invocation{Operation: "share", Arguments: []string{"credential", "get", store.item}}, bytes.NewReader(nil), &out, &errout, store); code != 0 {
		t.Fatalf("get code=%d diagnostics=%q", code, errout.String())
	}
	if out.String() != "private-value" {
		t.Fatalf("get returned %q", out.String())
	}
}

func TestCredentialProtocolRejectsInvalidInput(t *testing.T) {
	store := &testStore{}
	for _, args := range [][]string{
		{"credential", "put", "item", "--unknown"},
		{"credential", "get", "item", "extra"},
		{"credential", "put", "line\nbreak"},
	} {
		var out, errout bytes.Buffer
		if code := Run(Invocation{Operation: "share", Arguments: args}, strings.NewReader("secret"), &out, &errout, store); code != 2 || out.Len() != 0 {
			t.Fatalf("args=%q code=%d output=%q", args, code, out.String())
		}
	}
}
