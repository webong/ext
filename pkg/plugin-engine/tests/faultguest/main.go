// A deliberately broken command guest for host failure-path tests.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	mode := filepath.Base(os.Args[0])
	r := bufio.NewReader(os.Stdin)
	if _, err := r.ReadString('\n'); err != nil {
		return
	}
	if mode == "stall" {
		time.Sleep(time.Hour)
		return
	}
	if mode == "exit" {
		return
	}
	hello := `{"apiVersion":"ctx.plugin/v1","id":"hello","payload":{"apiVersion":"ctx.plugin/v1","identity":{"id":"ctx/conformance","revision":"fixture-1"},"contracts":[{"name":"ctx.conformance","version":"v1","operations":[{"name":"echo"},{"name":"wait"},{"name":"private-error"},{"name":"public-error"}]}]}}`
	if mode == "bad-handshake" {
		fmt.Println(`{"apiVersion":"ctx.plugin/v1","id":"hello","payload":{}}`)
		return
	}
	if mode == "fragment" {
		for _, b := range []byte(hello + "\n") {
			os.Stdout.Write([]byte{b})
		}
	} else {
		fmt.Println(hello)
	}
	if _, err := r.ReadString('\n'); err != nil {
		return
	}
	switch mode {
	case "duplicate":
		fmt.Println(`{"apiVersion":"ctx.plugin/v1","id":"1","payload":{"x":1,"\u0078":2}}`)
	case "wrong-id":
		fmt.Println(`{"apiVersion":"ctx.plugin/v1","id":"other","payload":null}`)
	case "unknown":
		fmt.Println(`{"apiVersion":"ctx.plugin/v1","id":"1","payload":null,"extra":true}`)
	case "oversize":
		fmt.Println(strings.Repeat("x", 24<<20+1))
	case "truncated":
		fmt.Print(`{"apiVersion":`)
	case "fragment":
		for _, b := range []byte(`{"apiVersion":"ctx.plugin/v1","id":"1","payload":{"value":7}}` + "\n") {
			os.Stdout.Write([]byte{b})
		}
	default:
		fmt.Println(`{"apiVersion":"ctx.plugin/v1","id":"1","error":{"code":"bad","message":"bad","retryAfterMilliseconds":-1}}`)
	}
}
