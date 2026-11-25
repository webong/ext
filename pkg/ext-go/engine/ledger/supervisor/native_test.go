package supervisor

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	ledger "github.com/webong/ext/pkg/ext-go/engine/ledger"
)

func TestNativeSupervisorChild(t *testing.T) {
	if os.Getenv("EXT_SUPERVISOR_FIXTURE") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}
func TestNativeSupervisor(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	n, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ledger.Bind(n)
	if err != nil {
		t.Fatal(err)
	}
	s, err := l.OpenStore(context.Background(), "memory", "", ledger.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sup, err := OpenNative(ctx, s, NativeOptions{RuntimeID: "native-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close()
	if ledger.Code(s.Close()) != "busy" {
		t.Fatal("store closed while supervisor alive")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := sup.Start(ctx, NativeSpec{Artifact: Artifact{ID: "fixture", Revision: "1"}, Command: executable, Args: []string{"-test.run=^TestNativeSupervisorChild$"}, Environment: map[string]string{"EXT_SUPERVISOR_FIXTURE": "1"}, AwaitReadiness: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	instance, err = sup.Await(ctx, instance.ID, StateRunning)
	if err != nil {
		t.Fatal(err)
	}
	if instance.PID <= 0 {
		t.Fatalf("no child: %+v", instance)
	}
	if err := sup.Ready(ctx, instance.ID, instance.PID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := sup.Await(ctx, instance.ID, StateReady); err != nil {
		t.Fatal(err)
	}
	if healthy, err := sup.Health(ctx, instance.ID, true); err != nil || !healthy {
		t.Fatalf("health %v %v", healthy, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := sup.Await(canceled, instance.ID, StateStopped); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := sup.Stop(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sup.Await(ctx, instance.ID, StateStopped); err != nil {
		t.Fatal(err)
	}
	if err := sup.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sup.Instances(ctx); !errors.Is(err, ledger.ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
