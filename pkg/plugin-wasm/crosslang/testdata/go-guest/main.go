package main

import (
	"context"
	"github.com/webong/ctx/pkg/plugin/jsonline"
	"github.com/webong/ctx/pkg/plugin/plugintest"
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
