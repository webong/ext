//go:build ignore

package main

import (
	"fmt"
	"unicode"
)

func main() {
	if unicode.Version != "17.0.0" {
		panic("requires Unicode 17.0.0")
	}
	fmt.Printf("/* Generated from Go unicode.ToLower, Unicode %s. */\nstatic const uint32_t ctx_lower[][2] = {\n", unicode.Version)
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if lo := unicode.ToLower(r); r != lo {
			fmt.Printf("{0x%x,0x%x},\n", r, lo)
		}
	}
	fmt.Println("};")
}
