package main

import (
	"context"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/inprocess"
	"github.com/webong/ext/pkg/plugin-go/plugintest"
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
