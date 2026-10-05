package sqlite

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSQLiteEmptyJSONQuery(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 unavailable")
	}
	database := filepath.Join(t.TempDir(), "cookies.sqlite")
	if _, err := RunSQLite(database, false, "CREATE TABLE cookies(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	output, err := RunSQLite(database, true, "SELECT value FROM cookies")
	if err != nil || strings.TrimSpace(string(output)) != "[]" {
		t.Fatalf("empty query: output=%q err=%v", output, err)
	}
	if _, err := RunSQLite(database, true, "SELECT value FROM missing_table"); err == nil {
		t.Fatal("invalid query became an empty result")
	}
}

func TestSQLiteNormalizesEmptyJSONOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, output := range []string{"", " \n\t", "[]\n", "[{\"value\":\"fixture\"}]\n"} {
		t.Run(strings.TrimSpace(output), func(t *testing.T) {
			t.Setenv("CTX_TEST_SQLITE_OUTPUT", output)
			script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' \"$CTX_TEST_SQLITE_OUTPUT\"\n"
			if err := os.WriteFile(filepath.Join(root, "sqlite3"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, readonly := range []bool{true, false} {
				result, err := RunSQLite(filepath.Join(root, "cookies.sqlite"), readonly, "SELECT value FROM cookies")
				want := output
				if readonly && strings.TrimSpace(output) == "" {
					want = "[]"
				}
				if err != nil || string(result) != want {
					t.Fatalf("readonly=%t: output=%q err=%v", readonly, result, err)
				}
			}
		})
	}
}

func TestSQLiteKeepsCookieValuesOffProcessArgumentsAndErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	arguments, input := filepath.Join(root, "arguments"), filepath.Join(root, "input")
	t.Setenv("CTX_TEST_SQLITE_ARGUMENTS", arguments)
	t.Setenv("CTX_TEST_SQLITE_INPUT", input)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$@" > "$CTX_TEST_SQLITE_ARGUMENTS"
cat > "$CTX_TEST_SQLITE_INPUT"
printf '%s' 'parse error: fixture-secret' >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(root, "sqlite3"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := RunSQLite(filepath.Join(root, "cookies.sqlite"), false, "INSERT INTO cookies(value) VALUES('fixture-secret')")
	if err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("write diagnostics exposed a cookie value")
	}
	argv, err := os.ReadFile(arguments)
	if err != nil || strings.Contains(string(argv), "fixture-secret") {
		t.Fatal("SQL appeared in process arguments")
	}
	data, err := os.ReadFile(input)
	if err != nil || !strings.Contains(string(data), "fixture-secret") {
		t.Fatal("SQL was not sent through stdin")
	}
}
