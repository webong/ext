package main

import (
	"context"
	"github.com/webong/ext/pkg/plugin-go/jsonline"
	"github.com/webong/ext/pkg/plugin-go/plugintest"
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
