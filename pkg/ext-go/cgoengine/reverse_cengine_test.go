//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"context"
	"testing"
)

func TestCEngineSessionDoneClosesWithTheSession(t *testing.T) {
	d := testDescriptor()
	s, err := Open(context.Background(), d, referenceOptions(&testBackend{d: d}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
		t.Fatal("Done closed before the session ended")
	default:
	}
	if err := s.Abort(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done did not close with the session")
	}
}
