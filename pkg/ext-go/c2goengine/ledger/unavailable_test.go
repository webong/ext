//go:build !ext_ledger_c2go || (!darwin && !linux) || (!amd64 && !arm64)

package ledger_test

import (
	"errors"
	"github.com/webong/ext/pkg/ext-go/c2goengine/ledger"
	"github.com/webong/ext/pkg/ext-go/core/driver"
	"testing"
)

func TestUnavailableDriverFailsExplicitly(t *testing.T) {
	l, err := ledger.Bind()
	if l != nil || !errors.Is(err, driver.ErrUnavailable) {
		t.Fatalf("got %v, %v; want unavailable without fallback", l, err)
	}
}
