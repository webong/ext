package sqlite

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func openFixture(t testing.TB, name string) *Database {
	t.Helper()
	db, err := Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return db
}

func scanAll(t testing.TB, db *Database, table string) []Row {
	t.Helper()
	var rows []Row
	err := db.Scan(table, func(row Row) error {
		copied := Row{RowID: row.RowID, columns: row.columns, values: append([]Value(nil), row.values...)}
		rows = append(rows, copied)
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", table, err)
	}
	return rows
}

func noRecoveredPanics(t *testing.T) {
	t.Helper()
	if n := recovered.Load(); n != 0 {
		t.Fatalf("%d internal panics were converted to errors; the reader must reject bad input explicitly", n)
	}
}

func TestChromiumShapedStore(t *testing.T) {
	db := openFixture(t, "chromium.db")
	if got := db.Tables(); len(got) != 2 || got[0] != "meta" || got[1] != "cookies" {
		t.Fatalf("tables %v (indexes must not be listed)", got)
	}
	columns, err := db.Columns("COOKIES")
	if err != nil || len(columns) != 20 || columns[1] != "host_key" || columns[19] != "has_cross_site_ancestor" {
		t.Fatalf("columns %v %v", columns, err)
	}
	rows := scanAll(t, db, "cookies")
	if len(rows) != 6 {
		t.Fatalf("%d rows", len(rows))
	}
	text := func(r Row, name string) string { v, _ := r.Text(name); return v }
	if text(rows[0], "host_key") != "example.com" || text(rows[0], "value") != "plain-value" || text(rows[0], "path") != "/" {
		t.Fatalf("row 0: %+v", rows[0])
	}
	if blob, ok := rows[1].Bytes("encrypted_value"); !ok || len(blob) != 19 || string(blob[:3]) != "v10" {
		t.Fatalf("encrypted_value %x %v", blob, ok)
	}
	if blob, ok := rows[0].Bytes("encrypted_value"); !ok || len(blob) != 0 {
		t.Fatalf("an empty blob must read as empty, got %x %v", blob, ok)
	}
	if text(rows[3], "value") != "café ☃" || text(rows[3], "path") != "/p q" {
		t.Fatalf("unicode row: %+v", rows[3])
	}
	if v, _ := rows[4].Int("expires_utc"); v != -5 {
		t.Fatalf("negative integer: %d", v)
	}
	if v, _ := rows[5].Int("creation_utc"); v != math.MaxInt64 {
		t.Fatalf("max int64: %d", v)
	}
	if v, _ := rows[5].Int("expires_utc"); v != 9007199254740993 {
		t.Fatalf("large integer lost precision: %d", v)
	}
	if v, ok := rows[5].Int("source_port"); !ok || v != 65535 {
		t.Fatalf("two-byte integer: %d", v)
	}
	if rows[2].RowID != 3 || rows[5].RowID != 6 {
		t.Fatalf("rowids %d %d", rows[2].RowID, rows[5].RowID)
	}
	// Lookup by rowid agrees with the scan.
	for _, want := range rows {
		got, ok, err := db.Lookup("cookies", want.RowID)
		if err != nil || !ok || fmt.Sprint(got.values) != fmt.Sprint(want.values) {
			t.Fatalf("lookup %d: %v %v %v", want.RowID, got.values, ok, err)
		}
	}
	if _, ok, err := db.Lookup("cookies", 99); ok || err != nil {
		t.Fatalf("missing rowid: %v %v", ok, err)
	}
	meta := scanAll(t, db, "meta")
	versions := map[string]string{}
	for _, row := range meta {
		k, _ := row.Text("key")
		v, _ := row.Text("value")
		versions[k] = v
	}
	if versions["version"] != "24" {
		t.Fatalf("meta %v", versions)
	}
	if _, err := db.Columns("missing"); !errors.Is(err, ErrNoTable) {
		t.Fatalf("missing table: %v", err)
	}
	noRecoveredPanics(t)
}

func TestFirefoxShapedStoreAliasesTheRowID(t *testing.T) {
	db := openFixture(t, "firefox.db")
	if v, err := db.UserVersion(); err != nil || v != 16 {
		t.Fatalf("user_version %d %v", v, err)
	}
	rows := scanAll(t, db, "moz_cookies")
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	var ids []int64
	for _, row := range rows {
		id, ok := row.Int("id")
		if !ok || id != row.RowID {
			t.Fatalf("INTEGER PRIMARY KEY must read as the rowid: %d vs %d", id, row.RowID)
		}
		ids = append(ids, id)
	}
	if fmt.Sprint(ids) != "[1 4 9]" {
		t.Fatalf("ids %v", ids)
	}
	if v, _ := rows[1].Text("originAttributes"); v != "^userContextId=2" {
		t.Fatalf("container attributes %q", v)
	}
	if v, _ := rows[2].Text("originAttributes"); v != "^partitionKey=%28https%2Ctop.example%29" {
		t.Fatalf("partition attributes %q", v)
	}
	if v, _ := rows[0].Int("expiry"); v != 1893456000000 {
		t.Fatalf("expiry %d", v)
	}
}

func TestMultiPageTreeScansInOrder(t *testing.T) {
	db := openFixture(t, "multipage.db")
	if db.pageSize != 512 || db.pages < 100 {
		t.Fatalf("fixture is not multi-page: page size %d pages %d", db.pageSize, db.pages)
	}
	rows := scanAll(t, db, "cookies")
	if len(rows) != 2500 {
		t.Fatalf("%d rows", len(rows))
	}
	for i, row := range rows {
		if row.RowID != int64(i+1) {
			t.Fatalf("row %d has rowid %d", i, row.RowID)
		}
		name, _ := row.Text("name")
		host, _ := row.Text("host_key")
		value, _ := row.Text("value")
		if name != "c"+strconv.Itoa(i+1) || host != fmt.Sprintf("h%d.example", (i+1)%7) || value != "v"+strconv.Itoa((i+1)*31%997) {
			t.Fatalf("row %d: %v", i, row.values)
		}
	}
	// The tree is three levels deep: the root's children are interior pages.
	info, _ := db.table("cookies")
	page, header, err := db.treePage(info.root)
	if err != nil || header.leaf {
		t.Fatalf("the root should be an interior page: %v", err)
	}
	cells, _ := db.cellOffsets(info.root, page, header)
	child, _, _ := interiorCell(page, cells[0])
	_, childHeader, err := db.treePage(child)
	if err != nil || childHeader.leaf {
		t.Fatalf("expected three levels, got a leaf under the root: %v", err)
	}
	for _, id := range []int64{1, 2, 13, 14, 250, 1250, 2499, 2500} {
		row, ok, err := db.Lookup("cookies", id)
		if err != nil || !ok || row.RowID != id {
			t.Fatalf("lookup %d: %v %v", id, ok, err)
		}
		if name, _ := row.Text("name"); name != "c"+strconv.FormatInt(id, 10) {
			t.Fatalf("lookup %d returned %q", id, name)
		}
	}
	for _, id := range []int64{0, -1, 2501, math.MaxInt64, math.MinInt64} {
		if _, ok, err := db.Lookup("cookies", id); ok || err != nil {
			t.Fatalf("lookup %d: %v %v", id, ok, err)
		}
	}
	// Stopping early stops the scan and returns the callback's error.
	stop := errors.New("stop")
	count := 0
	if err := db.Scan("cookies", func(Row) error { count++; return stop }); !errors.Is(err, stop) || count != 1 {
		t.Fatalf("early stop: %v after %d rows", err, count)
	}
	noRecoveredPanics(t)
}

func pattern(alphabet string, size int) string {
	return strings.Repeat(alphabet, size/len(alphabet)+1)[:size]
}

func TestOverflowChains(t *testing.T) {
	db := openFixture(t, "overflow.db")
	want := map[int64]struct {
		kind string
		size int
	}{1: {"blob", 2}, 2: {"text", 940}, 3: {"text", 960}, 4: {"text", 1000}, 5: {"text", 1700}, 6: {"text", 10000}, 7: {"text", 30000}, 8: {"blob", 10000}}
	rows := scanAll(t, db, "blobs")
	if len(rows) != len(want) {
		t.Fatalf("%d rows", len(rows))
	}
	for _, row := range rows {
		w := want[row.RowID]
		data, _ := row.Get("data")
		switch v := data.(type) {
		case string:
			if w.kind != "text" || len(v) != w.size || v != pattern("0123456789abcdef", w.size) {
				t.Errorf("row %d: text of %d bytes does not match the pattern (want %d)", row.RowID, len(v), w.size)
			}
		case []byte:
			if w.kind != "blob" || len(v) != w.size {
				t.Errorf("row %d: blob of %d bytes (want %d)", row.RowID, len(v), w.size)
			}
			if row.RowID == 8 && string(v) != pattern("ABCDEFGHIJKLMNOP", w.size) {
				t.Errorf("row 8: blob content differs")
			}
		default:
			t.Errorf("row %d: %T", row.RowID, data)
		}
	}
	if note, ok := rows[0].Text("note"); ok || note != "" {
		t.Fatalf("NULL must not read as text: %q %v", note, ok)
	}
	row, ok, err := db.Lookup("blobs", 7)
	if err != nil || !ok {
		t.Fatalf("lookup: %v %v", ok, err)
	}
	if v, _ := row.Text("data"); len(v) != 30000 {
		t.Fatalf("lookup of an overflow row returned %d bytes", len(v))
	}
	noRecoveredPanics(t)
}

func TestWriteAheadLogIsReplayed(t *testing.T) {
	values := func(db *Database) string {
		var parts []string
		for _, row := range scanAll(t, db, "cookies") {
			name, _ := row.Text("name")
			value, _ := row.Text("value")
			parts = append(parts, fmt.Sprintf("%d:%s=%s", row.RowID, name, value))
		}
		return strings.Join(parts, " ")
	}
	withWAL := openFixture(t, "wal.db")
	if got := values(withWAL); got != "2:two=updated-in-wal 3:three=from-wal 4:four=from-wal" {
		t.Fatalf("with the log: %s", got)
	}
	// Without the log the main file still shows the older checkpointed state.
	mainOnly, err := OpenBytes(fixture(t, "wal.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := values(mainOnly); got != "1:one=from-main-file 2:two=from-main-file" {
		t.Fatalf("main file only: %s", got)
	}
	if len(withWAL.frames) == 0 {
		t.Fatal("no frames were applied")
	}
	// Open picks up the -wal beside the database file.
	if _, err := os.Stat(filepath.Join("testdata", "wal.db-wal")); err != nil {
		t.Fatal(err)
	}
	noRecoveredPanics(t)
}

func TestWriteAheadLogIsValidatedFrameByFrame(t *testing.T) {
	database, wal := fixture(t, "wal.db"), fixture(t, "wal.db-wal")
	frame := 24 + 4096
	values := func(w []byte) string {
		db, err := OpenBytes(database, w)
		if err != nil {
			return "error: " + err.Error()
		}
		var parts []string
		_ = db.Scan("cookies", func(r Row) error {
			v, _ := r.Text("value")
			parts = append(parts, strconv.FormatInt(r.RowID, 10)+"="+v)
			return nil
		})
		return strings.Join(parts, ",")
	}
	full := values(wal)
	mutate := func(f func([]byte)) []byte {
		copied := append([]byte(nil), wal...)
		f(copied)
		return copied
	}
	cases := map[string][]byte{
		"truncated in the first frame":   wal[:32+frame-10],
		"empty log":                      nil,
		"header only":                    wal[:32],
		"bad magic is ignored":           mutate(func(b []byte) { b[0] ^= 0xff }),
		"bad header checksum is ignored": mutate(func(b []byte) { b[27] ^= 0xff }),
		"wrong version is ignored":       mutate(func(b []byte) { b[7] ^= 0x01 }),
		"corrupt frame data":             mutate(func(b []byte) { b[32+24+100] ^= 0xff }),
		"corrupt frame checksum":         mutate(func(b []byte) { b[32+23] ^= 0xff }),
		"wrong salt":                     mutate(func(b []byte) { b[32+9] ^= 0xff }),
		"zero page number":               mutate(func(b []byte) { copy(b[32:36], []byte{0, 0, 0, 0}) }),
	}
	main := values(nil)
	for name, w := range cases {
		got := values(w)
		if strings.HasPrefix(got, "error") {
			t.Errorf("%s: %s", name, got)
			continue
		}
		if got == full {
			t.Errorf("%s: a damaged log produced the full committed state", name)
		}
	}
	if cases := values(wal[:32+frame*2]); cases == full {
		t.Errorf("a log cut after frame 2 still shows the later commits: %s", cases)
	}
	// A log for a different page size is an error, not silently used.
	other := mutate(func(b []byte) { binary.BigEndian.PutUint32(b[8:12], 8192) })
	if got := values(other); !strings.HasPrefix(got, "error") {
		t.Errorf("mismatched page size: %s", got)
	}
	_ = main
	noRecoveredPanics(t)
}

func TestAddedColumnsReadAsTheirDefaults(t *testing.T) {
	db := openFixture(t, "altered.db")
	rows := scanAll(t, db, "cookies")
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	old, fresh := rows[0], rows[2]
	if v, _ := old.Int("level"); v != 7 {
		t.Fatalf("integer default: %d", v)
	}
	if v, _ := old.Text("note"); v != "n/a" {
		t.Fatalf("text default: %q", v)
	}
	if v, ok := old.Get("ratio"); !ok || v != -1.5 {
		t.Fatalf("real default: %v", v)
	}
	if v, ok := old.Get("extra"); !ok || v != nil {
		t.Fatalf("a column without a default is NULL: %v", v)
	}
	if v, _ := fresh.Int("level"); v != 1 {
		t.Fatalf("explicit value: %d", v)
	}
	if v, _ := fresh.Get("ratio"); v != 2.5 {
		t.Fatalf("real value: %v", v)
	}
	if v, _ := fresh.Int("extra"); v != 9 {
		t.Fatalf("last column: %d", v)
	}
}

func TestUnsupportedFeaturesAreReportedNotMisread(t *testing.T) {
	for name, table := range map[string]string{"withoutrowid.db": "cookies", "generated.db": "cookies"} {
		db, err := Open(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := db.Columns(table); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: columns: %v", name, err)
		}
		if err := db.Scan(table, func(Row) error { return nil }); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: scan: %v", name, err)
		}
		if _, _, err := db.Lookup(table, 1); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: lookup: %v", name, err)
		}
	}
	if _, err := Open(filepath.Join("testdata", "utf16.db")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("UTF-16 database: %v", err)
	}
}

func TestOpenDoesNotModifyOrCreateFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"wal.db", "wal.db-wal", "chromium.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), fixture(t, name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotDirectory(t, dir)
	for _, name := range []string{"wal.db", "chromium.db"} {
		db, err := Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		table := "cookies"
		if err := db.Scan(table, func(Row) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if after := snapshotDirectory(t, dir); !bytes.Equal(before, after) {
		t.Fatalf("the directory changed:\nbefore %s\nafter  %s", before, after)
	}
	if _, err := Open(filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("missing file opened")
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("directory opened")
	}
}

func snapshotDirectory(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		data, _ := os.ReadFile(filepath.Join(dir, name))
		fmt.Fprintf(&out, "%s %d %x\n", name, len(data), data[:min(len(data), 64)])
	}
	return out.Bytes()
}

func TestRowAccessors(t *testing.T) {
	row := Row{columns: []string{"A", "b", "c", "d", "e"}, values: []Value{int64(5), "text", []byte{1, 2}, 3.0, nil}}
	if v, ok := row.Int("a"); !ok || v != 5 {
		t.Fatal("case-insensitive lookup")
	}
	if v, ok := row.Int("D"); !ok || v != 3 {
		t.Fatal("integral real as integer")
	}
	if _, ok := row.Int("b"); ok {
		t.Fatal("text as integer")
	}
	if _, ok := row.Text("a"); ok {
		t.Fatal("integer as text")
	}
	if v, ok := row.Bytes("B"); !ok || string(v) != "text" {
		t.Fatal("text as bytes")
	}
	if _, ok := row.Bytes("e"); ok {
		t.Fatal("NULL as bytes")
	}
	if _, ok := row.Get("missing"); ok {
		t.Fatal("missing column")
	}
	if len(row.Columns()) != 5 {
		t.Fatal("columns")
	}
}

func TestSchemaParsing(t *testing.T) {
	cases := []struct {
		sql     string
		columns string
		alias   int
		err     bool
	}{
		{`CREATE TABLE t(a, b TEXT, c INTEGER NOT NULL)`, "a b c", -1, false},
		{`CREATE TABLE "my ""t""" ("A col" INTEGER PRIMARY KEY, [b] BLOB, ` + "`c`" + ` REAL)`, "A col|b|c", 0, false},
		{`CREATE TABLE t(a INTEGER, b INTEGER, PRIMARY KEY (b))`, "a b", 1, false},
		{`CREATE TABLE t(a INTEGER, b INTEGER, PRIMARY KEY (a, b))`, "a b", -1, false},
		{`CREATE TABLE t(a INT PRIMARY KEY, b)`, "a b", -1, false},
		{`CREATE TABLE t(a INTEGER PRIMARY KEY DESC, b)`, "a b", -1, false},
		{`CREATE TABLE t(a TEXT DEFAULT 'x,y)', b INTEGER CHECK (b > 0 AND b < 10), c DEFAULT (1+2)) -- trailing`, "a b c", -1, false},
		{`CREATE TABLE t(a VARCHAR(10), b DECIMAL(4,2) NOT NULL, CONSTRAINT u UNIQUE (a))`, "a b", -1, false},
		{`CREATE TABLE t(a /* comment, with comma */ INTEGER, b)`, "a b", -1, false},
		{`CREATE TABLE t(a INTEGER, b AS (a * 2))`, "", -1, true},
		{`CREATE TABLE t(a) WITHOUT ROWID`, "", -1, true},
		{`CREATE TABLE t`, "", -1, true},
		{`CREATE TABLE t(a, b`, "", -1, true},
		{`CREATE TABLE t(a 'unterminated)`, "", -1, true},
		{`CREATE TABLE t()`, "", -1, true},
		{`CREATE TABLE t(,)`, "", -1, true},
	}
	for _, c := range cases {
		info := &tableInfo{name: "t", aliasIdx: -1}
		err := parseCreateTable(c.sql, info)
		if (err != nil) != c.err {
			t.Errorf("%q: err=%v", c.sql, err)
			continue
		}
		if c.err {
			continue
		}
		want := strings.Fields(c.columns)
		if strings.Contains(c.columns, "|") {
			want = strings.Split(c.columns, "|")
		}
		if strings.Join(info.columns, "|") != strings.Join(want, "|") || info.aliasIdx != c.alias {
			t.Errorf("%q: columns %q alias %d, want %q alias %d", c.sql, info.columns, info.aliasIdx, want, c.alias)
		}
	}
	info := &tableInfo{name: "t", aliasIdx: -1}
	if err := parseCreateTable(`CREATE TABLE t(a DEFAULT -3, b DEFAULT 'x', c DEFAULT NULL, d DEFAULT (1+2), e DEFAULT 1.5, f DEFAULT TRUE, g DEFAULT 0x10)`, info); err != nil {
		t.Fatal(err)
	}
	want := []defaultValue{{true, int64(-3)}, {true, "x"}, {true, nil}, {}, {true, 1.5}, {true, int64(1)}, {true, int64(16)}}
	for i, w := range want {
		if info.defaults[i] != w {
			t.Errorf("default %d: %+v, want %+v", i, info.defaults[i], w)
		}
	}
}

// sqliteQuery runs the sqlite3 shell against a fresh copy of a fixture (and its
// -wal, when it has one), because the shell may checkpoint and remove a log.
func sqliteQuery(t *testing.T, _ string, database, query string) []map[string]any {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{database, database + "-wal"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("sqlite3", "-batch", "-json", filepath.Join(directory, database), query)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("sqlite3 %s %q: %v %s", database, query, err, stderr.String())
	}
	if len(bytes.TrimSpace(stdout.Bytes())) == 0 {
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("sqlite3 output: %v", err)
	}
	return rows
}

// canonical renders a value the way SQL quote() does, so values compare across
// both readers regardless of JSON typing.
func canonical(value Value) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return "R" + strconv.FormatFloat(v, 'g', 15, 64)
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case []byte:
		return fmt.Sprintf("X'%X'", v)
	}
	return "?"
}

// When the sqlite3 shell is installed, both readers must return identical rows
// for every committed fixture.
func TestAgreesWithTheSQLiteShell(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	directory := "" // each query works on its own copy
	names := []string{"chromium.db", "firefox.db", "multipage.db", "overflow.db", "wal.db", "altered.db"}
	for _, name := range names {
		db, err := Open(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range db.Tables() {
			columns, err := db.Columns(table)
			if err != nil {
				t.Fatalf("%s.%s: %v", name, table, err)
			}
			// Column names and order.
			info := sqliteQuery(t, directory, name, "PRAGMA table_info("+SQLIdentifier(table)+")")
			var want []string
			for _, column := range info {
				want = append(want, column["name"].(string))
			}
			if strings.Join(columns, ",") != strings.Join(want, ",") {
				t.Fatalf("%s.%s columns %v, shell says %v", name, table, columns, want)
			}
			// Row contents, rendered with quote() so types are unambiguous.
			var selects []string
			for _, column := range columns {
				selects = append(selects, "quote("+SQLIdentifier(column)+") AS "+SQLIdentifier(column))
			}
			shell := sqliteQuery(t, directory, name, "SELECT rowid AS __rowid, "+strings.Join(selects, ",")+" FROM "+SQLIdentifier(table)+" ORDER BY rowid")
			ours := scanAll(t, db, table)
			if len(ours) != len(shell) {
				t.Fatalf("%s.%s: %d rows, shell returns %d", name, table, len(ours), len(shell))
			}
			for i, row := range ours {
				if float64(row.RowID) != shell[i]["__rowid"].(float64) && fmt.Sprint(shell[i]["__rowid"]) != strconv.FormatInt(row.RowID, 10) {
					t.Fatalf("%s.%s row %d: rowid %d, shell %v", name, table, i, row.RowID, shell[i]["__rowid"])
				}
				for j, column := range columns {
					got := canonical(row.values[j])
					want := fmt.Sprint(shell[i][column])
					if strings.HasPrefix(got, "R") { // compare floats numerically
						g, _ := strconv.ParseFloat(got[1:], 64)
						w, err := strconv.ParseFloat(want, 64)
						if err != nil || g != w {
							t.Fatalf("%s.%s row %d %s: %s, shell %s", name, table, i, column, got, want)
						}
						continue
					}
					if got != want {
						t.Fatalf("%s.%s row %d %s: %.80s, shell %.80s", name, table, i, column, got, want)
					}
				}
			}
		}
	}
}

func TestBoundsAreEnforced(t *testing.T) {
	big := make([]byte, MaxDatabaseBytes+1)
	if _, err := OpenBytes(big, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("oversize database: %v", err)
	}
	if _, err := OpenBytes(fixture(t, "chromium.db"), make([]byte, MaxWALBytes+1)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("oversize log: %v", err)
	}
	if MaxDatabaseBytes != 512<<20 || MaxWALBytes != 256<<20 {
		t.Fatal("bounds are documented API")
	}
}

func TestOpenSharedReusesAnUnchangedSnapshotAndNoticesChanges(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "cookies.db")
	if err := os.WriteFile(path, fixture(t, "chromium.db"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := OpenShared(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenShared(path)
	if err != nil || first != second {
		t.Fatalf("an unchanged file must reuse the snapshot: %p %p %v", first, second, err)
	}
	// A different size is a different snapshot, and the new contents are read.
	other := fixture(t, "firefox.db")
	if err := os.WriteFile(path, other, 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := OpenShared(path)
	if err != nil || third == first {
		t.Fatalf("a changed file must be read again: %v", err)
	}
	if _, err := third.Columns("moz_cookies"); err != nil {
		t.Fatalf("the new contents were not used: %v", err)
	}
	// A write-ahead log appearing beside the file changes the snapshot too.
	walPath := filepath.Join(directory, "wal.db")
	if err := os.WriteFile(walPath, fixture(t, "wal.db"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := OpenShared(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(walPath+"-wal", fixture(t, "wal.db-wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := OpenShared(walPath)
	if err != nil || after == before || len(after.frames) == 0 {
		t.Fatalf("the log was not picked up: %v", err)
	}
	// The cache stays small.
	for i := 0; i < 10; i++ {
		p := filepath.Join(directory, fmt.Sprintf("copy%d.db", i))
		if err := os.WriteFile(p, fixture(t, "chromium.db"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenShared(p); err != nil {
			t.Fatal(err)
		}
	}
	sharedMu.Lock()
	size := len(sharedCache)
	sharedMu.Unlock()
	if size > 4 {
		t.Fatalf("cache holds %d entries", size)
	}
	if _, err := OpenShared(filepath.Join(directory, "missing.db")); err == nil {
		t.Fatal("missing file")
	}
}

func TestCookieHostCandidates(t *testing.T) {
	got := CookieHostCandidates("a.b.example.com")
	want := []string{"a.b.example.com", ".a.b.example.com", "b.example.com", ".b.example.com", "example.com", ".example.com", "com", ".com"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v", got)
	}
	if CookieHostSQL("example.com") != "'example.com','.example.com','com','.com'" {
		t.Fatalf("%s", CookieHostSQL("example.com"))
	}
	if len(CookieHostCandidates("")) != 0 {
		t.Fatal("empty host")
	}
}

func TestCookieDatabaseColumnsUsesTheBuiltInReader(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no sqlite3 shell
	columns, err := CookieDatabaseColumns(filepath.Join("testdata", "firefox.db"), "moz_cookies")
	if err != nil || len(columns) < 10 || columns[0] != "id" {
		t.Fatalf("%v %v", columns, err)
	}
	if _, err := CookieDatabaseColumns(filepath.Join("testdata", "firefox.db"), "missing"); err == nil {
		t.Fatal("missing table")
	}
}
