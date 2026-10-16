// A deliberately broken command guest for host failure-path tests.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	// A grandchild that holds the inherited stderr open long after its parent is gone.
	if os.Getenv("FAULT_GRANDCHILD") == "1" {
		time.Sleep(10 * time.Second) // outlives the host's destroy, but never the test run
		return
	}
	// The mode is the name this binary was started under; Windows adds ".exe".
	mode := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
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
	if mode == "grandchild" {
		gc := exec.Command(os.Args[0])
		gc.Env = append(os.Environ(), "FAULT_GRANDCHILD=1")
		gc.Stderr = os.Stderr
		if err := gc.Start(); err != nil {
			return
		}
	}
	hello := `{"apiVersion":"ext.plugin/v1","id":"hello","payload":{"apiVersion":"ext.plugin/v1","identity":{"id":"ctx/conformance","revision":"fixture-1"},"contracts":[{"name":"ext.conformance","version":"v1","operations":[{"name":"echo"},{"name":"wait"},{"name":"private-error"},{"name":"public-error"}]}]}}`
	if mode == "bad-handshake" {
		fmt.Println(`{"apiVersion":"ext.plugin/v1","id":"hello","payload":{}}`)
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
		fmt.Println(`{"apiVersion":"ext.plugin/v1","id":"1","payload":{"x":1,"\u0078":2}}`)
	case "wrong-id":
		fmt.Println(`{"apiVersion":"ext.plugin/v1","id":"other","payload":null}`)
	case "unknown":
		fmt.Println(`{"apiVersion":"ext.plugin/v1","id":"1","payload":null,"extra":true}`)
	case "oversize":
		fmt.Println(strings.Repeat("x", 24<<20+1))
	case "truncated":
		fmt.Print(`{"apiVersion":`)
	case "fragment":
		for _, b := range []byte(`{"apiVersion":"ext.plugin/v1","id":"1","payload":{"value":7}}` + "\n") {
			os.Stdout.Write([]byte{b})
		}
	default:
		fmt.Println(`{"apiVersion":"ext.plugin/v1","id":"1","error":{"code":"bad","message":"bad","retryAfterMilliseconds":-1}}`)
	}
}
