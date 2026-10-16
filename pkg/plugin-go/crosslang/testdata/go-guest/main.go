package main

import (
	"context"
	"github.com/webong/ext/pkg/plugin-go/contract/jsonline"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest"
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
