//go:build cgo && ext_ledger_cgo

package ledger_test

import (
	"context"
	native "github.com/webong/ext/pkg/ext-go/cgoengine/ledger"
	model "github.com/webong/ext/pkg/ext-go/engine/ledger"
	"github.com/webong/ext/pkg/ext-go/engine/ledger/ledgertest"
	"testing"
)

func TestLinkedLedgerConformance(t *testing.T) {
	l, err := native.Bind()
	if err != nil {
		t.Fatal(err)
	}
	b := ledgertest.Backend{Durable: true, Open: func(t testing.TB, dir string, o model.Options) model.Store {
		s, err := l.OpenStore(context.Background(), "file", dir, o)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}}
	ledgertest.Run(t, b)
	ledgertest.RunBehaviour(t, b)
}
