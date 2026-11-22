package main

import (
	"context"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/plugintest"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/inprocess"
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
