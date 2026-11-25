//go:build !ext_ledger_c2go || (!darwin && !linux) || (!amd64 && !arm64)

package ledger

import (
	"fmt"
	model "github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/core/driver"
)

func Bind() (*model.Library, error) {
	return nil, fmt.Errorf("ledger: generate the C translation and build with ext_ledger_c2go on supported 64-bit POSIX targets: %w", driver.ErrUnavailable)
}
