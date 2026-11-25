// A product-neutral fixture adapter using the real credential client.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/webong/ext/res/system/service/credential/client"
)

func run() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(os.Getenv("EXT_HOME"), "snapshot-paths"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(log, executable)
	closeErr := log.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	lock := filepath.Join(os.Getenv("EXT_HOME"), ".package-write.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		return err
	}
	if err := os.Remove(lock); err != nil {
		return err
	}
	value, err := client.GetDeclared(context.Background(), "credential", "alpha")
	if err != nil {
		return err
	}
	if slices.Contains(os.Args, "query") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"cookies": []map[string]string{{"name": "session", "value": string(value), "domain": "example.test", "path": "/"}}})
	}
	_, err = os.Stdout.Write(value)
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
