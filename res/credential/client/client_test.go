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
	root := t.TempDir()
	host := filepath.Join(root, "ctx")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = credential ] && [ \"$2\" = get ] && " +
		"[ \"$3\" = 'fixture:service=ctx&account=work' ] && [ \"$4\" = --stdout ] || exit 2\n" +
		"printf 'synthetic-secret'\n"
	if err := os.WriteFile(host, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CTX_EXECUTABLE", host)
	t.Setenv("CTX_DEPENDENCY_CREDENTIAL", "fixture")
	value, err := GetDeclared(context.Background(), "credential", "service=ctx&account=work")
	if err != nil || string(value) != "synthetic-secret" {
		t.Fatalf("credential=%q err=%v", value, err)
	}
	t.Setenv("CTX_DEPENDENCY_CREDENTIAL", "")
	if _, err := GetDeclared(context.Background(), "credential", "service=ctx&account=work"); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("missing dependency: %v", err)
	}
}
