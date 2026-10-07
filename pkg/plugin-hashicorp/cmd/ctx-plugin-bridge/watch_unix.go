//go:build darwin || linux || freebsd

package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/webong/ext/pkg/plugin"
)

// The worker survives launcher death and owns the actual go-plugin client.
// Closing its private liveness pipe cancels serving and kills/reaps the guest.
func watchParent(c config) error {
	// Register the inherited pipe with Go's poller so Close interrupts Read.
	// A blocking inherited fd otherwise leaves shutdown waiting on its reader.
	if err := syscall.SetNonblock(3, true); err != nil {
		return plugin.ErrInvalid
	}
	life := os.NewFile(3, "ctx-parent-liveness")
	if life == nil {
		return plugin.ErrInvalid
	}
	defer life.Close()
	info, err := life.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return plugin.ErrInvalid
	}
	syscall.CloseOnExec(3)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	lost := make(chan struct{})
	go func() { defer close(lost); _, _ = io.Copy(io.Discard, life); cancel() }()
	err = serve(ctx, c)
	_ = life.Close()
	<-lost
	return err
}
