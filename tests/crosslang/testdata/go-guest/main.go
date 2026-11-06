package main

import (
	"context"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
	"log"
)

func main() {
	g, err := plugintest.Guest()
	if err != nil {
		log.Fatal(err)
	}
	if err := jsonline.ServeStdio(context.Background(), g); err != nil {
		log.Fatal(err)
	}
}
