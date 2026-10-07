//go:build !windows

package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/webong/ext/pkg/graph"
)

func TestProcessTreeHelper(t *testing.T) {
	if os.Getenv("EXT_TREE_HELPER") != "1" {
		return
	}
	child := exec.Command("sh", "-c", "sleep 1; printf done > \"$1\"", "sh", os.Getenv("EXT_TREE_SENTINEL"))
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	_ = os.WriteFile(os.Getenv("EXT_TREE_STARTED"), []byte("started"), 0600)
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func TestStopTerminatesDescendantProcesses(t *testing.T) {
	store := graph.NewMemory()
	defer store.Close()
	sup, err := New(Options{Graph: store})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close(context.Background())
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	sentinel := filepath.Join(dir, "descendant-survived")
	instance, err := sup.Start(context.Background(), Spec{
		Artifact: Artifact{ID: "tree", Revision: "1"}, Command: os.Args[0], Args: []string{"-test.run=TestProcessTreeHelper"},
		Environment: map[string]string{"EXT_TREE_HELPER": "1", "EXT_TREE_STARTED": started, "EXT_TREE_SENTINEL": sentinel},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
	if err := sup.Stop(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("descendant survived process-tree stop: %v", err)
	}
}
