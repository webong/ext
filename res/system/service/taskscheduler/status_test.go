package taskscheduler

import (
	"errors"
	"runtime"
	"testing"
)

func TestTaskStatusParsesNativeListOutput(t *testing.T) {
	if !taskStatus([]byte("TaskName:      Example Agent\r\nStatus:        Running\r\n")) {
		t.Fatal("running task was not recognized")
	}
	if taskStatus([]byte("TaskName:      Example Agent\r\nStatus:        Ready\r\n")) {
		t.Fatal("ready task was reported as running")
	}
	if taskStatus([]byte("Status:        Ready\r\nStatus:        Running\r\n")) {
		t.Fatal("first status line should decide")
	}
	if taskStatus([]byte("localized output without a status field")) {
		t.Fatal("unknown output was reported as running")
	}
}

func TestNewPlatformAvailability(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := New(Config{Directory: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := New(Config{Directory: t.TempDir()}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("non-Windows taskscheduler = %v", err)
	}
}
