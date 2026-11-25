//go:build !cgo || !ext_ledger_cgo

package ledger

import (
	"errors"
	model "github.com/webong/ext/pkg/ext-go/engine/ledger"
)

func Bind() (*model.Library, error) {
	return nil, errors.New("ledger: linked C driver requires cgo and the ext_ledger_cgo build tag")
}
