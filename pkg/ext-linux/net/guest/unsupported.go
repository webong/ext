//go:build !linux || !cgo

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "Linux network guest requires Linux and cgo")
	os.Exit(2)
}
