package packagelock

import (
	"os"
	"os/exec"
	"testing"
)

func TestCrossProcessExclusion(t *testing.T) {
	if home := os.Getenv("EXT_TEST_LOCK_HOME"); home != "" {
		release, err := Acquire(home)
		if err == nil {
			release()
			os.Exit(2)
		}
		os.Exit(0)
	}
	home := t.TempDir()
	release, err := Acquire(home)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrossProcessExclusion$")
	cmd.Env = append(os.Environ(), "EXT_TEST_LOCK_HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child acquired held lock: %v %s", err, out)
	}
	release()
	again, err := Acquire(home)
	if err != nil {
		t.Fatalf("released lock unavailable: %v", err)
	}
	again()
}
