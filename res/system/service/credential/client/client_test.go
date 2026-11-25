//go:build !windows

package client

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetDeclaredUsesHostProtocol(t *testing.T) {
	t.Setenv("EXT_PRODUCT_PREFIX", "")
	root := t.TempDir()
	host := filepath.Join(root, "ctx")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = credential ] && [ \"$2\" = get ] && " +
		"[ \"$3\" = 'fixture:service=ctx&account=work' ] && [ \"$4\" = --stdout ] || exit 2\n" +
		"printf 'synthetic-secret'\n"
	if err := os.WriteFile(host, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXT_EXECUTABLE", host)
	t.Setenv("EXT_DEPENDENCY_CREDENTIAL", "fixture")
	value, err := GetDeclared(context.Background(), "credential", "service=ctx&account=work")
	if err != nil || string(value) != "synthetic-secret" {
		t.Fatalf("credential=%q err=%v", value, err)
	}
	t.Setenv("EXT_DEPENDENCY_CREDENTIAL", "")
	if _, err := GetDeclared(context.Background(), "credential", "service=ctx&account=work"); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("missing dependency: %v", err)
	}
}

func TestGetDeclaredUsesProductPrefix(t *testing.T) {
	host := filepath.Join(t.TempDir(), "ext")
	script := `#!/bin/sh
[ "$#" = 5 ] && [ "$1" = ctx ] && [ "$2" = credential ] && [ "$3" = get ] && [ "$4" = fixture:alpha ] && [ "$5" = --stdout ] || exit 2
printf synthetic-secret
`
	if err := os.WriteFile(host, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXT_EXECUTABLE", host)
	t.Setenv("EXT_PRODUCT_PREFIX", "ctx")
	t.Setenv("EXT_DEPENDENCY_CREDENTIAL", "fixture")
	value, err := GetDeclared(context.Background(), "credential", "alpha")
	if err != nil || string(value) != "synthetic-secret" {
		t.Fatalf("prefixed call: %q %v", value, err)
	}
	if _, err := GetDeclaredWithEnv(context.Background(), "credential", "alpha", map[string]string{"EXT_PRODUCT_PREFIX": "other"}); err == nil {
		t.Fatal("native selector settings changed host prefix")
	}
	t.Setenv("EXT_PRODUCT_PREFIX", "ctx --other")
	if _, err := GetDeclared(context.Background(), "credential", "alpha"); err == nil || !strings.Contains(err.Error(), "prefix") {
		t.Fatalf("invalid prefix: %v", err)
	}
}
