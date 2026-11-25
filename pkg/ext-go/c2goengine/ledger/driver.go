//go:build ext_ledger_c2go && (darwin || linux) && (amd64 || arm64)

// Package ledger binds the canonical ledger through translated C.
package ledger

import (
	"github.com/webong/ext/pkg/ext-go/c2goengine/ledger/internal/ccgo"
	model "github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func Bind() (*model.Library, error) {
	api, err := library.FromDriver(ccgo.Driver{}, "ext_ledger", 1)
	if err != nil {
		return nil, err
	}
	return model.BindInterface(api)
}
