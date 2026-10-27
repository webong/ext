package app

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClipboardCommandExplainsMissingBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows backend touches the real clipboard")
	}
	var stdout, stderr bytes.Buffer
	if code := clipboardCommand([]string{"generation"}, &stdout, &stderr); code != 2 {
		t.Fatalf("generation = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no Go clipboard backend") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestReadBoundedRejectsOversizedFile(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "small")
	if err := os.WriteFile(small, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readBounded(small, 16)
	if err != nil || string(data) != "value" {
		t.Fatalf("bounded read = %q, %v", data, err)
	}
	large := filepath.Join(root, "large")
	if err := os.WriteFile(large, []byte("too large"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = readBounded(large, 4); err == nil {
		t.Fatal("oversized clipboard file was accepted")
	}
	if _, err = readBounded(filepath.Join(root, "missing"), 4); err == nil {
		t.Fatal("missing clipboard file was accepted")
	}
}
