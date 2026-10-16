package main

import (
	"context"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
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
