//go:build !darwin && !linux

package pkgcmd

import (
	"fmt"
	"os"
)

func duplicateFile(n int) (*os.File, error) {
	return nil, fmt.Errorf("numeric descriptor %d is unavailable", n)
}
