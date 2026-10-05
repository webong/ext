package app

import (
	"bytes"
	"context"
	"github.com/webong/ctx/pkg/graph"
	systemgraph "github.com/webong/ctx/pkg/graph/system"
	"testing"
)

func TestGraphProcessCLIValidation(t *testing.T) {
	g, err := systemgraph.New(graph.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for _, tc := range []struct {
		command string
		args    []string
		code    int
	}{
		{"process", nil, 2}, {"process", []string{"0"}, 2}, {"process", []string{"-1"}, 2}, {"process", []string{"2147483648"}, 2},
		{"process", []string{"123", "--resource-limit", "0"}, 2}, {"process", []string{"123", "--limit", "1"}, 2},
		{"processes", []string{"--limit", "65537"}, 2}, {"processes", []string{"--timeout", "0s"}, 2}, {"processes", []string{"extra"}, 2},
		{"process", []string{"--help"}, 0}, {"processes", []string{"--help"}, 0},
	} {
		var stdout, stderr bytes.Buffer
		if got := graphProcessCommand(context.Background(), g, tc.command, tc.args, &stdout, &stderr); got != tc.code {
			t.Fatalf("%s %v: code=%d stderr=%s", tc.command, tc.args, got, stderr.String())
		}
	}
	snapshot, err := g.Store.Snapshot(context.Background(), systemgraph.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Vertices) != 0 {
		t.Fatal("help or invalid arguments collected process data")
	}
}
