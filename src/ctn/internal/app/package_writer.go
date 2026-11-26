package app

import (
	"fmt"
	"path/filepath"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/home"
	"github.com/webong/ext/res/system/device/dirlock"
)

// The trust ledger always belongs to EXT_HOME, including when an older caller
// overrides the intelligence store. Use the same boundary as ext package and
// native package_writer consumers throughout publication and trust changes.
func acquirePackageWriter() (func(), error) {
	release, err := dirlock.Acquire(filepath.Join(home.Config(), ".package-write.lock"))
	if err != nil {
		return nil, fmt.Errorf("ctn: package %w", err)
	}
	return func() { _ = release() }, nil
}
