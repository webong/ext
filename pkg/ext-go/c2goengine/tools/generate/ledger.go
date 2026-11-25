package generate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// generateLedger translates the canonical store and recorder. Host collection
// and process supervision require native kernels and are explicitly unsupported
// by this profile; no Go implementation is substituted.
func generateLedger(ctx context.Context, log io.Writer, root, out string) error {
	if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return fmt.Errorf("ledger translation: unsupported target %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if err := checkRuntimeVersion(root, libcVersion); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "ext-ledger-translate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	for _, dir := range []string{"include", "src"} {
		if err := copyTree(filepath.Join(root, "lib/ledger", dir), filepath.Join(work, dir)); err != nil {
			return err
		}
	}
	if err := copyFS(shims, "shim", filepath.Join(work, "shim")); err != nil {
		return err
	}
	desc, err := os.ReadFile(filepath.Join(root, "lib/ledger/interface.json"))
	if err != nil {
		return err
	}
	var description strings.Builder
	description.WriteString("#include <stddef.h>\nconst char ll_interface_description[] = {")
	for _, b := range desc {
		fmt.Fprintf(&description, "%d,", b)
	}
	description.WriteString("0};\nconst size_t ll_interface_description_len = sizeof ll_interface_description - 1;\n")
	if err := os.WriteFile(filepath.Join(work, "description.c"), []byte(description.String()), 0644); err != nil {
		return err
	}
	if err := prepareTranslationModule(work, "ledger/generate"); err != nil {
		return err
	}
	args := []string{"ccgo", "-o", "ledger.go", "--package-name", "ccgo", "-I", "src", "-I", "include", "-I", "shim", "-DEXT_LEDGER_STATIC", "-DEXT_LEDGER_PORTABLE", "-include", "shim/compat_core.h"}
	cmake, err := os.ReadFile(filepath.Join(root, "lib/ledger/CMakeLists.txt"))
	if err != nil {
		return err
	}
	match := regexp.MustCompile(`(?s)set\(ledger_portable_sources\s+([^)]*)\)`).FindSubmatch(cmake)
	if match == nil {
		return fmt.Errorf("ledger_portable_sources missing from CMake")
	}
	args = append(args, strings.Fields(string(match[1]))...)
	args = append(args, "src/platform_posix.c", "src/sqlite_none.c")

	args = append(args, "description.c")
	if err := runCCGO(ctx, work, args, log); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(work, "ledger.go"))
	if err != nil {
		return err
	}
	data = bytes.Replace(data, []byte("//go:build "), []byte("//go:build ext_ledger_c2go && "), 1)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	return os.WriteFile(out, data, 0644)
}
