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

type sqliteColumn struct {
	Name string `json:"name"`
}

func CookieHostSQL(host string) string {
	var hosts []string
	for part := host; part != ""; {
		hosts = append(hosts, SQLString(part), SQLString("."+part))
		dot := strings.IndexByte(part, '.')
		if dot < 0 {
			break
		}
		part = part[dot+1:]
	}
	return strings.Join(hosts, ",")
}

func CookieDatabaseColumns(database, table string) ([]string, error) {
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

// ReadableCookieDatabase takes a private snapshot when a read-only SQLite
// connection cannot open a profile's active WAL database.
func ReadableCookieDatabase(database, table string) (string, func(), []string, error) {
	columns, err := CookieDatabaseColumns(database, table)
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
	columns, snapshotErr = CookieDatabaseColumns(snapshot, table)
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
