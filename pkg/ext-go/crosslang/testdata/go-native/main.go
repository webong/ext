package main

import (
	"context"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
)

func CTXPlugin(ctx context.Context) (plugin.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	guest, err := plugintest.Guest()
	if err != nil {
		return nil, err
	}
	return inprocess.New(guest)
}
