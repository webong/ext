package capability

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"syscall"
	"testing"
)

func TestUnavailableReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no error", nil, NoDevice},
		{"permission", fs.ErrPermission, Permission},
		{"wrapped access errno", fmt.Errorf("read: %w", syscall.EACCES), Permission},
		{"tool not found", &exec.Error{Name: "tool", Err: exec.ErrNotFound}, ToolMissing},
		{"missing source", fs.ErrNotExist, NoDevice},
		{"other failure", errors.New("native call failed"), NoDevice},
	}
	for _, c := range cases {
		got := Unavailable(c.err)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if !Allowed("unavailable", got) {
			t.Errorf("%s: reason %q is not allowed for unavailable", c.name, got)
		}
	}
}
