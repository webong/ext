package process

import (
	"errors"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/plugin"
)

func TestWindowsLaunchPathNeedsExecutableExtension(t *testing.T) {
	for _, ok := range []string{`C:\p\bin\provider.exe`, `C:\p\bin\Provider.EXE`, `C:\p\run.cmd`} {
		if err := checkLaunchable("windows", ok); err != nil {
			t.Fatalf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{`C:\p\bin\provider`, `C:\p\main.py`} {
		err := checkLaunchable("windows", bad)
		if !errors.Is(err, plugin.ErrInvalid) || !strings.Contains(err.Error(), "extension") {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	if err := checkLaunchable("linux", "/p/bin/provider"); err != nil {
		t.Fatalf("other systems launch extensionless files: %v", err)
	}
}
