//go:build !cgo

package main

import "log"

func main() { log.Fatal("the C shared guest requires CGO_ENABLED=1 and -buildmode=c-shared") }
