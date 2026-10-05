package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testRepo(t *testing.T) (string, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is not installed")
	}
	root := t.TempDir()
	cmd := exec.Command(git, "init", "-q", root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	return git, root
}

func runHooks(t *testing.T, git string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := hooks(git, args, &out)
	return out.String(), err
}

func TestInstallRemoveGeneratedHook(t *testing.T) {
	git, root := testRepo(t)
	if _, err := runHooks(t, git, "install", "pre-commit", "--", "git", "config", "--local", "ctx.hook-test", "yes"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".git", "hooks", "pre-commit")
	commit := exec.Command(git, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "hook-test")
	if output, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("commit hook failed: %v: %s", err, output)
	}
	value, err := gitOutput(git, "config", "--get", "ctx.hook-test")
	if err != nil || value != "yes" {
		t.Fatalf("command did not run: %q, %v", value, err)
	}
	status, err := runHooks(t, git, "status", "pre-commit")
	if err != nil || !strings.Contains(status, "pre-commit: ctx-managed") {
		t.Fatalf("status = %q, %v", status, err)
	}
	if _, err := runHooks(t, git, "remove", "pre-commit"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("generated hook remains: %v", err)
	}
}

func TestExistingHookRestored(t *testing.T) {
	git, root := testRepo(t)
	path := filepath.Join(root, ".git", "hooks", "pre-commit")
	original := []byte("#!/bin/sh\necho existing\n")
	if err := os.WriteFile(path, original, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := runHooks(t, git, "install", "pre-commit", "--", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := runHooks(t, git, "remove", "pre-commit"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("existing hook changed: %q, %v", got, err)
	}
}

func TestFailingHookStopsCommit(t *testing.T) {
	git, _ := testRepo(t)
	if _, err := runHooks(t, git, "install", "pre-commit", "--", "false"); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command(git, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "should-fail")
	if output, err := commit.CombinedOutput(); err == nil {
		t.Fatalf("failing hook allowed commit: %s", output)
	}
}

func TestConfiguredHookDirectoryRequiresExplicitTarget(t *testing.T) {
	git, root := testRepo(t)
	cmd := exec.Command(git, "config", "core.hooksPath", ".husky/_")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, output)
	}
	if err := os.MkdirAll(filepath.Join(root, ".husky", "_"), 0755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(root, ".husky", "_", "pre-commit")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\necho wrapper\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := runHooks(t, git, "install", "pre-commit", "--", "true"); err == nil {
		t.Fatal("modified configured manager's hook without explicit target")
	}
	source := filepath.Join(root, ".husky", "pre-commit")
	original := []byte("echo source\n")
	if err := os.WriteFile(source, original, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := runHooks(t, git, "install", "--directory", ".husky", "pre-commit", "--", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := runHooks(t, git, "remove", "--directory", ".husky", "pre-commit"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("source hook changed: %q, %v", got, err)
	}
	got, err = os.ReadFile(wrapper)
	if err != nil || string(got) != "#!/bin/sh\necho wrapper\n" {
		t.Fatalf("wrapper changed: %q, %v", got, err)
	}
}

func TestRejectSymlinkAndNonShellHook(t *testing.T) {
	git, root := testRepo(t)
	path := filepath.Join(root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := runHooks(t, git, "install", "pre-commit", "--", "true"); err == nil {
		t.Fatal("accepted non-shell hook")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := runHooks(t, git, "install", "pre-commit", "--", "true"); err == nil {
		t.Fatal("accepted symlink hook")
	}
}
