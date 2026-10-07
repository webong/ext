//go:build ext_cengine

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin-go"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	args := os.Args
	if len(args) < 4 {
		return fmt.Errorf("expected guest descriptor requests")
	}
	b, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	var d plugin.Descriptor
	if err = json.Unmarshal(b, &d); err != nil {
		return err
	}
	allow := func([]byte) error { return nil }
	h, err := goengine.New(args[1], d, allow, allow)
	if err != nil {
		return err
	}
	defer h.Destroy()
	if err = h.Start(context.Background()); err != nil {
		return err
	}
	for _, file := range args[3:] {
		b, err = os.ReadFile(file)
		if err != nil {
			return err
		}
		out, err := h.CallRaw(context.Background(), b)
		if err != nil {
			return err
		}
		fmt.Println(string(out))
	}
	return nil
}
