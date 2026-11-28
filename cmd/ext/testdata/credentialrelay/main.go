// A product-neutral fixture adapter using the real credential client.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/webong/ext/src/ctx/system/service/credential/client"
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
	if !slices.Contains(os.Args, "query") {
		args := []string{"real", "scopefixture"}
		if prefix := os.Getenv("EXT_PRODUCT_PREFIX"); prefix != "" {
			args = append([]string{prefix}, args...)
		}
		resolved, err := exec.CommandContext(context.Background(), os.Getenv("EXT_EXECUTABLE"), args...).Output()
		if err != nil {
			return fmt.Errorf("nested program lookup: %w", err)
		}
		if filepath.Base(strings.TrimSpace(string(resolved))) != "scopefixture" {
			return fmt.Errorf("wrong native program: %q", resolved)
		}
	}
	if _, err := client.GetDeclaredWithEnv(context.Background(), "credential", "alpha", map[string]string{"PATH": "/untrusted"}); err == nil {
		return fmt.Errorf("broker accepted process-loader settings")
	}
	value, err := client.GetDeclaredWithEnv(context.Background(), "credential", "alpha", map[string]string{"EXT_SNAPSHOT_LEAF_SELECTOR": "fixture"})
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
