package main

import (
	"context"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
	"log"
)

// The reference guest of the frames suite (plugintest.FramesGuest) as a process,
// for the cross-language suite to host in the same way as the foreign guests.
func main() {
	g, err := plugintest.FramesGuest()
	if err != nil {
		log.Fatal(err)
	}
	if err := jsonline.ServeStdio(context.Background(), g); err != nil {
		log.Fatal(err)
	}
}
