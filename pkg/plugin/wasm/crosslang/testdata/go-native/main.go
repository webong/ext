package main

import (
	"context"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/inprocess"
	"github.com/webong/ctx/pkg/plugin/plugintest"
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
