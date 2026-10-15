package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// CookieHostCandidates returns the host_key values a cookie for host can have:
// the host and each parent domain, each with and without a leading dot.
func CookieHostCandidates(host string) []string {
	var hosts []string
	for part := host; part != ""; {
		hosts = append(hosts, part, "."+part)
		dot := strings.IndexByte(part, '.')
		if dot < 0 {
			break
		}
		part = part[dot+1:]
	}
	return hosts
}

func CookieHostSQL(host string) string {
	candidates := CookieHostCandidates(host)
	quoted := make([]string, len(candidates))
	for i, candidate := range candidates {
		quoted[i] = SQLString(candidate)
	}
	return strings.Join(quoted, ",")
}

type sqliteColumn struct {
	Name string `json:"name"`
}

// cliColumns lists a table's columns through the sqlite3 shell. It is how
// ReadableCookieDatabase learns whether the shell itself can open a database.
func cliColumns(database, table string) ([]string, error) {
	output, err := RunSQLite(database, true, "PRAGMA table_info("+SQLIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	var columns []sqliteColumn
	if err := json.Unmarshal(output, &columns); err != nil || len(columns) == 0 {
		return nil, fmt.Errorf("cookie table %s is unavailable", table)
	}
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.Name)
	}
	return names, nil
}

// CookieDatabaseColumns returns the column names of a table, read with the
// built-in reader (no sqlite3 binary needed).
func CookieDatabaseColumns(database, table string) ([]string, error) {
	db, err := Open(database)
	if err != nil {
		return nil, err
	}
	columns, err := db.Columns(table)
	if err != nil {
		return nil, fmt.Errorf("cookie table %s is unavailable", table)
	}
	return columns, nil
}

// ReadableCookieDatabase is for callers that run SQL through RunSQLite and so
// need a path the sqlite3 shell can open. The cookie readers in the adapters no
// longer use it: they read through Open, which needs no shell. It takes a private snapshot when a read-only SQLite
// connection cannot open a profile's active WAL database.
func ReadableCookieDatabase(database, table string) (string, func(), []string, error) {
	columns, err := cliColumns(database, table)
	if err == nil {
		return database, func() {}, columns, nil
	}
	if !strings.Contains(err.Error(), "unable to open database file") && !strings.Contains(err.Error(), "attempt to write a readonly database") {
		return "", nil, nil, err
	}
	snapshot, cleanup, snapshotErr := SnapshotCookieDatabase(database)
	if snapshotErr != nil {
		return "", nil, nil, fmt.Errorf("cannot read cookie database: %w", snapshotErr)
	}
	columns, snapshotErr = cliColumns(snapshot, table)
	if snapshotErr != nil {
		cleanup()
		return "", nil, nil, snapshotErr
	}
	return snapshot, cleanup, columns, nil
}

func SnapshotCookieDatabase(database string) (string, func(), error) {
	for attempt := 0; attempt < 3; attempt++ {
		directory, err := os.MkdirTemp("", "ctx-cookies-")
		if err != nil {
			return "", nil, err
		}
		cleanup := func() { _ = os.RemoveAll(directory) }
		wal := database + "-wal"
		beforeDB, err := os.Stat(database)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		beforeWAL, walErr := os.Stat(wal)
		if walErr != nil && !errors.Is(walErr, os.ErrNotExist) {
			cleanup()
			return "", nil, walErr
		}
		snapshot := filepath.Join(directory, filepath.Base(database))
		if err := CopyPrivateFile(database, snapshot); err != nil {
			cleanup()
			return "", nil, err
		}
		if walErr == nil {
			if err := CopyPrivateFile(wal, snapshot+"-wal"); err != nil {
				cleanup()
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return "", nil, err
			}
		}
		afterDB, dbErr := os.Stat(database)
		afterWAL, afterWalErr := os.Stat(wal)
		if dbErr == nil && sameFileVersion(beforeDB, afterDB) &&
			((errors.Is(walErr, os.ErrNotExist) && errors.Is(afterWalErr, os.ErrNotExist)) ||
				(walErr == nil && afterWalErr == nil && sameFileVersion(beforeWAL, afterWAL))) {
			return snapshot, cleanup, nil
		}
		cleanup()
	}
	return "", nil, errors.New("cookie database changed while taking a private snapshot; retry after the browser is idle")
}

func sameFileVersion(before, after os.FileInfo) bool {
	return os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime() == after.ModTime()
}

func CopyPrivateFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func HasSQLiteColumn(columns []string, wanted string) bool {
	for _, column := range columns {
		if column == wanted {
			return true
		}
	}
	return false
}

func RunSQLite(database string, readonly bool, query string) ([]byte, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, errors.New("sqlite3 is required for browser cookie sharing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"-batch", "-bail", "-json"}
	if readonly {
		args = append(args, "-readonly")
	}
	args = append(args, database)
	command := exec.CommandContext(ctx, "sqlite3", args...)
	// Imports can contain plaintext cookie values. Keep SQL off the process
	// command line and use a private stdin pipe instead.
	command.Stdin = strings.NewReader(query + "\n;\n")
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("sqlite3 timed out while accessing browser cookies")
		}
		if !readonly {
			// SQLite parse diagnostics can repeat the statement, including values.
			return nil, fmt.Errorf("sqlite3 update failed: %w", err)
		}
		if failure, ok := err.(*exec.ExitError); ok {
			message := strings.TrimSpace(string(failure.Stderr))
			if message != "" {
				return nil, fmt.Errorf("sqlite3: %s", message)
			}
		}
		return nil, fmt.Errorf("sqlite3 failed: %w", err)
	}
	if !utf8.Valid(output) {
		return nil, errors.New("sqlite3 returned invalid UTF-8 browser data")
	}
	// SQLite's JSON mode emits nothing for a query with no rows on some
	// versions. Readers must receive the same empty array on every platform.
	if readonly && len(bytes.TrimSpace(output)) == 0 {
		return []byte("[]"), nil
	}
	return output, nil
}

func SQLString(value string) string     { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func SQLIdentifier(value string) string { return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\"" }
func SQLBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
