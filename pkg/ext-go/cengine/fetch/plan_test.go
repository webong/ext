package fetch_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/fetch"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func TestPlanAndSharedArchiveRoots(t *testing.T) {
	data := archive(t, entry{name: "first/lib/native.so", body: "first target"}, entry{name: "second/lib/native.so", body: "second target"})
	c := library.Citation{Schema: library.CitationSchema, Library: "ext_example", Version: "1.0.0"}
	for i, target := range []string{"linux/amd64", "darwin/arm64"} {
		root := []string{"first", "second"}[i]
		c.Assets = append(c.Assets, library.Asset{Targets: []string{target}, URL: "https://example.invalid/shared.tar.gz", SHA256: digestOf(data), Root: root, Drivers: library.Drivers{PureGo: &library.PureGoFiles{Shared: "lib/native.so"}}})
	}
	plan, err := fetch.Plan(c, []string{fetch.PureGo}, []string{"linux/amd64", "darwin/arm64"})
	if err != nil || len(plan) != 2 || plan[0].Asset.Root != "first" || plan[1].Asset.Root != "second" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	for _, pair := range []struct{ drivers, targets []string }{
		{[]string{fetch.PureGo, fetch.PureGo}, []string{"linux/amd64"}},
		{[]string{fetch.PureGo}, []string{"linux/amd64", "linux/amd64"}},
		{[]string{"unknown"}, []string{"linux/amd64"}},
		{nil, []string{"linux/amd64"}},
		{[]string{fetch.PureGo}, nil},
	} {
		if _, err := fetch.Plan(c, pair.drivers, pair.targets); err == nil {
			t.Fatalf("accepted invalid plan: %+v", pair)
		}
	}
	if _, err := fetch.Plan(c, []string{fetch.CGo}, []string{"linux/amd64"}); !errors.Is(err, library.ErrNoAsset) {
		t.Fatalf("missing driver: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "generated")
	calls := 0
	err = fetch.Fetch(context.Background(), fetch.Options{Citation: c, Drivers: []string{fetch.PureGo}, Targets: []string{"linux/amd64", "darwin/arm64"}, Dir: dir, Cache: t.TempDir(), Fetch: func(context.Context, string) (io.ReadCloser, error) {
		calls++
		return io.NopCloser(bytes.NewReader(data)), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("shared archive downloaded %d times", calls)
	}
	for target, want := range map[string]string{"linux_amd64": "first target", "darwin_arm64": "second target"} {
		got, err := os.ReadFile(filepath.Join(dir, "lib", target, "native.so"))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", target, got, err)
		}
	}
}
