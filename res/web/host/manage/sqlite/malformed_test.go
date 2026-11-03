package sqlite

import (
	"encoding/binary"
	"errors"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A tiny SQLite image builder, so tests can craft hostile b-trees that the
// sqlite3 shell would never write.

func putVarint(value uint64) []byte {
	if value <= 0x7f {
		return []byte{byte(value)}
	}
	if value > 1<<56-1 {
		out := make([]byte, 9)
		out[8] = byte(value)
		value >>= 8
		for i := 7; i >= 0; i-- {
			out[i] = byte(value&0x7f) | 0x80
			value >>= 7
		}
		return out
	}
	var reversed []byte
	reversed = append(reversed, byte(value&0x7f))
	for value >>= 7; value > 0; value >>= 7 {
		reversed = append(reversed, byte(value&0x7f)|0x80)
	}
	out := make([]byte, len(reversed))
	for i, b := range reversed {
		out[len(reversed)-1-i] = b
	}
	return out
}

func record(values ...any) []byte {
	var header, body []byte
	for _, value := range values {
		switch v := value.(type) {
		case nil:
			header = append(header, 0)
		case int:
			header = append(header, 6)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v))
			body = append(body, b[:]...)
		case string:
			header = append(header, putVarint(uint64(len(v))*2+13)...)
			body = append(body, v...)
		case []byte:
			header = append(header, putVarint(uint64(len(v))*2+12)...)
			body = append(body, v...)
		}
	}
	size := len(header) + 1
	return append(append(putVarint(uint64(size)), header...), body...)
}

func leafCell(rowid int64, payload []byte) []byte {
	cell := putVarint(uint64(len(payload)))
	cell = append(cell, putVarint(uint64(rowid))...)
	return append(cell, payload...)
}

func interiorCellBytes(child uint32, key int64) []byte {
	cell := make([]byte, 4)
	binary.BigEndian.PutUint32(cell, child)
	return append(cell, putVarint(uint64(key))...)
}

// treePageImage lays out a b-tree page: header, cell pointers, cells from the end.
func treePageImage(pageSize, headerOffset int, leaf bool, cells [][]byte, rightmost uint32) []byte {
	page := make([]byte, pageSize)
	if leaf {
		page[headerOffset] = 0x0d
	} else {
		page[headerOffset] = 0x05
		binary.BigEndian.PutUint32(page[headerOffset+8:], rightmost)
	}
	binary.BigEndian.PutUint16(page[headerOffset+3:], uint16(len(cells)))
	headerSize := 8
	if !leaf {
		headerSize = 12
	}
	end := pageSize
	for i, cell := range cells {
		end -= len(cell)
		copy(page[end:], cell)
		binary.BigEndian.PutUint16(page[headerOffset+headerSize+2*i:], uint16(end))
	}
	binary.BigEndian.PutUint16(page[headerOffset+5:], uint16(end))
	return page
}

// image assembles a database whose table "t(a,b)" is rooted at page 2.
func image(pageSize int, tableSQL string, pages ...[]byte) []byte {
	schema := leafCell(1, record("table", "t", "t", 2, tableSQL))
	first := treePageImage(pageSize, 100, true, [][]byte{schema}, 0)
	copy(first, "SQLite format 3\x00")
	size := uint16(pageSize)
	if pageSize == 65536 {
		size = 1
	}
	binary.BigEndian.PutUint16(first[16:], size)
	first[18], first[19] = 1, 1
	first[21], first[22], first[23] = 64, 32, 32
	binary.BigEndian.PutUint32(first[28:], uint32(1+len(pages)))
	binary.BigEndian.PutUint32(first[56:], 1)
	out := first
	for _, page := range pages {
		out = append(out, page...)
	}
	return out
}

func exercise(t *testing.T, name string, data, wal []byte) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		db, err := OpenBytes(data, wal)
		if err != nil {
			done <- err
			return
		}
		var last error
		for _, table := range db.Tables() {
			if err := db.Scan(table, func(Row) error { return nil }); err != nil {
				last = err
			}
			for _, id := range []int64{0, 1, 2, 1 << 40, -1} {
				if _, _, err := db.Lookup(table, id); err != nil {
					last = err
				}
			}
			_, _ = db.Columns(table)
		}
		_, _ = db.UserVersion()
		done <- last
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: the reader did not finish", name)
		return nil
	}
}

func TestHostileTreesAreRejected(t *testing.T) {
	const size = 1024
	leaf := func(cells ...[]byte) []byte { return treePageImage(size, 0, true, cells, 0) }
	okRow := leafCell(1, record(1, "x"))
	cases := map[string][]byte{
		"interior page pointing at itself":                 image(size, "CREATE TABLE t(a,b)", treePageImage(size, 0, false, [][]byte{interiorCellBytes(2, 10)}, 2)),
		"interior pointing back at the schema page":        image(size, "CREATE TABLE t(a,b)", treePageImage(size, 0, false, nil, 1)),
		"child page out of range":                          image(size, "CREATE TABLE t(a,b)", treePageImage(size, 0, false, [][]byte{interiorCellBytes(999, 10)}, 998)),
		"child page zero":                                  image(size, "CREATE TABLE t(a,b)", treePageImage(size, 0, false, [][]byte{interiorCellBytes(0, 10)}, 0)),
		"index page used as a table":                       image(size, "CREATE TABLE t(a,b)", func() []byte { p := leaf(okRow); p[0] = 0x0a; return p }()),
		"unknown page type":                                image(size, "CREATE TABLE t(a,b)", func() []byte { p := leaf(okRow); p[0] = 0x77; return p }()),
		"cell count larger than the page":                  image(size, "CREATE TABLE t(a,b)", func() []byte { p := leaf(okRow); binary.BigEndian.PutUint16(p[3:], 65535); return p }()),
		"cell pointer into the pointer array":              image(size, "CREATE TABLE t(a,b)", func() []byte { p := leaf(okRow); binary.BigEndian.PutUint16(p[8:], 9); return p }()),
		"cell pointer past the page":                       image(size, "CREATE TABLE t(a,b)", func() []byte { p := leaf(okRow); binary.BigEndian.PutUint16(p[8:], uint16(size+10)); return p }()),
		"cell pointer into the reserved area":              image(size, "CREATE TABLE t(a,b)", func() []byte { p := leaf(okRow); binary.BigEndian.PutUint16(p[8:], uint16(size-20)); return p }()),
		"payload size larger than any record":              image(size, "CREATE TABLE t(a,b)", leaf(append(putVarint(1<<40), append(putVarint(1), 0)...))),
		"payload size with an overflow that never arrives": image(size, "CREATE TABLE t(a,b)", leaf(append(append(putVarint(5000), putVarint(1)...), make([]byte, 960)...))),
		"truncated cell":                                   image(size, "CREATE TABLE t(a,b)", leaf([]byte{0x81})),
		"record header longer than payload":                image(size, "CREATE TABLE t(a,b)", leaf(leafCell(1, []byte{0x40, 1, 2}))),
		"record header size zero":                          image(size, "CREATE TABLE t(a,b)", leaf(leafCell(1, []byte{0, 1, 2}))),
		"reserved serial type":                             image(size, "CREATE TABLE t(a,b)", leaf(leafCell(1, []byte{2, 10}))),
		"serial type promises a huge blob":                 image(size, "CREATE TABLE t(a,b)", leaf(leafCell(1, append([]byte{5}, append(putVarint(1<<50), 1)...)))),
		"body shorter than the serial types":               image(size, "CREATE TABLE t(a,b)", leaf(leafCell(1, []byte{2, 6}))),
		"text that is not UTF-8":                           image(size, "CREATE TABLE t(a,b)", leaf(leafCell(1, []byte{2, 15, 0xff, 0xfe}))),
		"schema with a bad rootpage":                       image(size, "CREATE TABLE t(a,b)"),
		"schema statement without columns":                 image(size, "CREATE TABLE t", leaf(okRow)),
		"schema statement unterminated":                    image(size, "CREATE TABLE t(a, 'b", leaf(okRow)),
	}
	// A chain of interior pages deeper than the limit.
	var chain [][]byte
	for i := 0; i < 40; i++ {
		chain = append(chain, treePageImage(size, 0, false, nil, uint32(3+i)))
	}
	chain = append(chain, leaf(okRow))
	cases["interior chain deeper than the limit"] = image(size, "CREATE TABLE t(a,b)", chain...)
	// An overflow chain that loops, and one that ends early.
	loopCell := append(append(putVarint(5000), putVarint(1)...), make([]byte, 0)...)
	local := (&Database{usable: size - 32}).localPayload(5000)
	loopCell = append(loopCell, make([]byte, local)...)
	loopCell = append(loopCell, 0, 0, 0, 3) // next overflow page is page 3
	overflowLoop := make([]byte, size)
	binary.BigEndian.PutUint32(overflowLoop, 3) // page 3 points at itself
	cases["overflow chain that loops"] = image(size, "CREATE TABLE t(a,b)", leaf(loopCell), overflowLoop)
	// A record with more columns than the limit allows.
	wide := make([]byte, 0, 70000)
	wide = append(wide, putVarint(5001)...)
	wide = append(wide, make([]byte, 5000)...)
	cases["record with thousands of columns"] = image(65536, "CREATE TABLE t(a,b)", treePageImage(65536, 0, true, [][]byte{leafCell(1, wide)}, 0))

	for name, data := range cases {
		err := exercise(t, name, data, nil)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: unexpected error type: %v", name, err)
		}
	}
	noRecoveredPanics(t)
}

func TestBuilderMakesReadableDatabases(t *testing.T) {
	const size = 1024
	rows := [][]byte{leafCell(1, record(10, "x")), leafCell(2, record(nil, []byte{1, 2, 3})), leafCell(5, record("é", 7))}
	db, err := OpenBytes(image(size, "CREATE TABLE t(a,b)", treePageImage(size, 0, true, rows, 0)), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := scanAll(t, db, "t")
	if len(got) != 3 || canonical(got[0].values[0]) != "10" || canonical(got[1].values[1]) != "X'010203'" || canonical(got[2].values[0]) != "'é'" {
		t.Fatalf("%v", got)
	}
	// The interior case: two leaves under one interior page.
	left := treePageImage(size, 0, true, rows[:2], 0)
	right := treePageImage(size, 0, true, rows[2:], 0)
	root := treePageImage(size, 0, false, [][]byte{interiorCellBytes(3, 2)}, 4)
	db, err = OpenBytes(image(size, "CREATE TABLE t(a,b)", root, left, right), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows := scanAll(t, db, "t"); len(rows) != 3 || rows[2].RowID != 5 {
		t.Fatalf("%d rows", len(rows))
	}
	if row, ok, err := db.Lookup("t", 5); err != nil || !ok || canonical(row.values[1]) != "7" {
		t.Fatalf("lookup %v %v", ok, err)
	}
}

// A header that claims more pages than the file holds is harmless until a page
// beyond the file is needed; then it is an error, never an out-of-range read.
func TestPageCountBeyondTheFile(t *testing.T) {
	good := fixture(t, "multipage.db")
	claimed := append([]byte(nil), good...)
	binary.BigEndian.PutUint32(claimed[28:], 1<<30)
	binary.BigEndian.PutUint32(claimed[24:], binary.BigEndian.Uint32(claimed[92:]))
	db, err := OpenBytes(claimed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows := scanAll(t, db, "cookies"); len(rows) != 2500 {
		t.Fatalf("%d rows", len(rows))
	}
	cut, err := OpenBytes(claimed[:len(claimed)/2], nil)
	if err == nil {
		err = cut.Scan("cookies", func(Row) error { return nil })
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("a truncated file must fail when a page is missing: %v", err)
	}
	noRecoveredPanics(t)
}

func TestHostileHeadersAreRejected(t *testing.T) {
	good := fixture(t, "chromium.db")
	mutate := func(f func([]byte) []byte) []byte { return f(append([]byte(nil), good...)) }
	set16 := func(offset int, v uint16) func([]byte) []byte {
		return func(b []byte) []byte { binary.BigEndian.PutUint16(b[offset:], v); return b }
	}
	set32 := func(offset int, v uint32) func([]byte) []byte {
		return func(b []byte) []byte { binary.BigEndian.PutUint32(b[offset:], v); return b }
	}
	cases := map[string][]byte{
		"empty":                    nil,
		"99 bytes":                 good[:99],
		"header only":              good[:100],
		"bad magic":                mutate(func(b []byte) []byte { b[0] = 'X'; return b }),
		"page size 0":              mutate(set16(16, 0)),
		"page size 3":              mutate(set16(16, 3)),
		"page size 256":            mutate(set16(16, 256)),
		"page size 1000":           mutate(set16(16, 1000)),
		"page size 65535":          mutate(set16(16, 65535)),
		"reserved space too large": mutate(func(b []byte) []byte { b[20] = 255; return b }),
		"unknown file format":      mutate(func(b []byte) []byte { b[18] = 9; return b }),
		"UTF-16 text":              mutate(set32(56, 2)),
		"file cut mid page":        good[:len(good)-1000],
		"first page only":          good[:4096],
		"first page zeroed": mutate(func(b []byte) []byte {
			for i := 100; i < 4096; i++ {
				b[i] = 0
			}
			return b
		}),
		"first page garbage": mutate(func(b []byte) []byte {
			for i := 100; i < 4096; i++ {
				b[i] = 0xff
			}
			return b
		}),
	}
	for name, data := range cases {
		err := exercise(t, name, data, nil)
		if err == nil {
			// A cut file with intact pages may legitimately still open; it must
			// never return data from beyond the file.
			if name != "file cut mid page" {
				t.Errorf("%s: accepted", name)
			}
			continue
		}
	}
	noRecoveredPanics(t)
}

// Deterministic corruption of every fixture: single-byte, run and truncation
// mutations must end in an error or in rows, never a panic, hang, or large
// allocation. The fuzz target extends this search.
func TestMutatedFixturesNeverPanic(t *testing.T) {
	random := rand.New(rand.NewSource(20260501))
	names := []string{"chromium.db", "firefox.db", "multipage.db", "overflow.db", "wal.db", "altered.db"}
	for _, name := range names {
		original := fixture(t, name)
		wal := []byte(nil)
		if name == "wal.db" {
			wal = fixture(t, "wal.db-wal")
		}
		rounds := 400
		if strings.HasPrefix(name, "multipage") || strings.HasPrefix(name, "overflow") {
			rounds = 120 // larger files; keep the test quick
		}
		for i := 0; i < rounds; i++ {
			data := append([]byte(nil), original...)
			logImage := append([]byte(nil), wal...)
			switch i % 4 {
			case 0: // flip a header byte
				data[random.Intn(120)] ^= byte(1 << uint(random.Intn(8)))
			case 1: // flip a byte anywhere
				data[random.Intn(len(data))] = byte(random.Intn(256))
			case 2: // overwrite a run
				start := random.Intn(len(data))
				for j := start; j < len(data) && j < start+1+random.Intn(64); j++ {
					data[j] = byte(random.Intn(256))
				}
			case 3: // truncate
				data = data[:random.Intn(len(data))]
				if len(logImage) > 0 {
					logImage = logImage[:random.Intn(len(logImage))]
				}
			}
			_ = exercise(t, name, data, logImage)
		}
	}
	noRecoveredPanics(t)
}

func TestMutatedWriteAheadLogNeverPanics(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	database, wal := fixture(t, "wal.db"), fixture(t, "wal.db-wal")
	for i := 0; i < 500; i++ {
		damaged := append([]byte(nil), wal...)
		for j := 0; j <= i%5; j++ {
			damaged[random.Intn(len(damaged))] = byte(random.Intn(256))
		}
		_ = exercise(t, "wal", database, damaged)
	}
	noRecoveredPanics(t)
}

func TestHostileFilesOnDisk(t *testing.T) {
	// A directory, a missing file and a device-like empty file are errors.
	dir := t.TempDir()
	if _, err := Open(filepath.Join(dir, "none")); err == nil {
		t.Fatal("missing file")
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("directory")
	}
}

// Cells that share one overflow chain would make a small file cost
// cells x payload to read. The per-operation budget stops that, while the same
// chain used once is read normally.
func TestSharedOverflowChainsAreBounded(t *testing.T) {
	const size = 4096
	chunk := size - 4 // usable bytes per overflow page
	payload := 15 * chunk
	local := (&Database{usable: size}).localPayload(payload)
	// One blob column holding payload-4 zero bytes: the record header is
	// 1 + 3 bytes, so the whole payload is a valid record.
	valid := append(append([]byte{4}, putVarint(uint64(payload-4)*2+12)...), make([]byte, payload)...)
	cell := func(rowid int64, chainStart uint32) []byte {
		c := append(putVarint(uint64(payload)), putVarint(uint64(rowid))...)
		c = append(c, valid[:local]...)
		return append(c, byte(chainStart>>24), byte(chainStart>>16), byte(chainStart>>8), byte(chainStart))
	}
	chain := func() [][]byte { // 15 distinct pages, the last one ending the chain
		var pages [][]byte
		for i := 0; i < 15; i++ {
			page := make([]byte, size)
			if i < 14 {
				binary.BigEndian.PutUint32(page, uint32(i)+1) // patched below to absolute numbers
			}
			pages = append(pages, page)
		}
		return pages
	}
	link := func(pages [][]byte, first uint32) {
		for i := range pages {
			if i < len(pages)-1 {
				binary.BigEndian.PutUint32(pages[i], first+uint32(i)+1)
			}
		}
	}

	// Ten leaves of seven cells (pages 3 to 12), all claiming one chain at page 13.
	var leaves, interior [][]byte
	rowid := int64(1)
	for leaf := 0; leaf < 10; leaf++ {
		var cells [][]byte
		for i := 0; i < 7; i++ {
			cells = append(cells, cell(rowid, 13))
			rowid++
		}
		leaves = append(leaves, treePageImage(size, 0, true, cells, 0))
		interior = append(interior, interiorCellBytes(uint32(3+leaf), rowid-1))
	}
	root := treePageImage(size, 0, false, interior[:len(interior)-1], uint32(3+len(interior)-1))
	shared := chain()
	link(shared, 13)
	pages := append(append([][]byte{root}, leaves...), shared...)
	db, err := OpenBytes(image(size, "CREATE TABLE t(a,b)", pages...), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Scan("t", func(Row) error { return nil })
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("cells that all claim the same overflow chain were read in full: %v", err)
	}

	// One cell using its own chain reads fine, with all of its bytes.
	own := chain()
	link(own, 3)
	db, err = OpenBytes(image(size, "CREATE TABLE t(a,b)", append([][]byte{treePageImage(size, 0, true, [][]byte{cell(1, 3)}, 0)}, own...)...), nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	if err := db.Scan("t", func(r Row) error {
		if blob, _ := r.Bytes("a"); len(blob) != payload-4 {
			t.Errorf("blob of %d bytes, want %d", len(blob), payload-4)
		}
		rows++
		return nil
	}); err != nil || rows != 1 {
		t.Fatalf("a chain used once must read in full: %d rows, %v", rows, err)
	}
	noRecoveredPanics(t)
}

func TestOverflowChainThatRepeatsAPageIsRejected(t *testing.T) {
	const size = 1024
	chunk := size - 4
	payload := 6 * chunk
	local := (&Database{usable: size}).localPayload(payload)
	cell := append(putVarint(uint64(payload)), putVarint(1)...)
	cell = append(cell, make([]byte, local)...)
	cell = append(cell, 0, 0, 0, 3)
	// Page 3 -> page 4 -> page 3 ...: the chain revisits a page.
	three, four := make([]byte, size), make([]byte, size)
	binary.BigEndian.PutUint32(three, 4)
	binary.BigEndian.PutUint32(four, 3)
	db, err := OpenBytes(image(size, "CREATE TABLE t(a,b)", treePageImage(size, 0, true, [][]byte{cell}, 0), three, four), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Scan("t", func(Row) error { return nil }); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("a repeating chain was followed: %v", err)
	}
	noRecoveredPanics(t)
}

func TestValueSlotBudget(t *testing.T) {
	info := &tableInfo{name: "t", aliasIdx: -1, columns: make([]string, 100), defaults: make([]defaultValue, 100)}
	for i := range info.defaults {
		info.defaults[i] = defaultValue{known: true}
	}
	db := &Database{}
	budget := &work{slots: 250}
	for i := 0; i < 2; i++ {
		if _, err := db.decodeRow(info, 1, []byte{1}, budget); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	if _, err := db.decodeRow(info, 1, []byte{1}, budget); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("the third row exceeds the budget: %v", err)
	}
}

// No input may cost more than a small fixed amount of time. Pathological schema
// text (huge, repetitive, deeply nested) and thousands of structure-aware
// mutations of every fixture each finish well inside the bound. A fuzz run that
// appears to stall is the Go fuzz engine minimizing a large input, not the reader;
// see the fuzz notes in testdata/README.md.
func TestNoInputIsSlow(t *testing.T) {
	const bound = 250 * time.Millisecond
	schemas := map[string]string{
		"repeated primary key": "CREATE TABLE t(a, PRIMARY KEY " + strings.Repeat("PRIMARY KEY ", 40000) + ")",
		"many columns":         "CREATE TABLE t(" + strings.Repeat("a,", 30000) + "b)",
		"many comments":        "CREATE TABLE t(a " + strings.Repeat("/* x */", 40000) + ")",
		"many strings":         "CREATE TABLE t(a " + strings.Repeat("'x' ", 40000) + ")",
		"nested parentheses":   "CREATE TABLE t(a CHECK " + strings.Repeat("(", 30000) + strings.Repeat(")", 30000) + ")",
		"repeated default":     "CREATE TABLE t(a " + strings.Repeat("DEFAULT 1 ", 40000) + ")",
		"one huge word":        "CREATE TABLE t(" + strings.Repeat("x", 900000) + ")",
	}
	for name, statement := range schemas {
		start := time.Now()
		_ = parseCreateTable(statement, &tableInfo{name: "t", aliasIdx: -1})
		if elapsed := time.Since(start); elapsed > bound {
			t.Errorf("schema %q took %v", name, elapsed)
		}
	}
	random := rand.New(rand.NewSource(99))
	seeds := [][2][]byte{}
	for _, name := range []string{"chromium.db", "firefox.db", "multipage.db", "overflow.db", "altered.db", "wal.db"} {
		var log []byte
		if name == "wal.db" {
			log = fixture(t, "wal.db-wal")
		}
		seeds = append(seeds, [2][]byte{fixture(t, name), log})
	}
	var worst time.Duration
	for i := 0; i < 20000; i++ {
		seed := seeds[random.Intn(len(seeds))]
		data, log := append([]byte(nil), seed[0]...), append([]byte(nil), seed[1]...)
		for k := 0; k <= random.Intn(6); k++ {
			switch random.Intn(5) {
			case 0:
				data[random.Intn(len(data))] = byte(random.Intn(256))
			case 1:
				data[random.Intn(min(len(data), 200))] ^= byte(1 << uint(random.Intn(8)))
			case 2: // clobber cell pointers or counts on a random page
				at := random.Intn(len(data)/512)*512 + 3 + random.Intn(45)
				if at+1 < len(data) {
					data[at], data[at+1] = byte(random.Intn(256)), byte(random.Intn(256))
				}
			case 3:
				if len(log) > 0 {
					log[random.Intn(len(log))] = byte(random.Intn(256))
				}
			case 4:
				a, b := random.Intn(len(data)), random.Intn(len(data))
				copy(data[a:], data[b:min(len(data), b+random.Intn(300))])
			}
		}
		start := time.Now()
		db, err := OpenBytes(data, log)
		if err == nil {
			for _, table := range db.Tables() {
				_ = db.Scan(table, func(Row) error { return nil })
				_, _, _ = db.Lookup(table, int64(random.Intn(3000)))
			}
		}
		if elapsed := time.Since(start); elapsed > worst {
			worst = elapsed
		}
	}
	if worst > bound {
		t.Fatalf("the slowest of 20000 mutated inputs took %v (bound %v)", worst, bound)
	}
	t.Logf("slowest of 20000 mutated inputs: %v", worst)
	noRecoveredPanics(t)
}
