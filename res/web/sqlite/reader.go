package sqlite

// A read-only reader for the part of the SQLite file format that browser cookie
// stores need: the database header, table b-trees (interior and leaf pages),
// varint records, overflow pages, and write-ahead log replay. It does not parse
// SQL beyond the column list of a table's CREATE statement, evaluates no
// queries, and never writes. The whole database is read into memory once (a
// bounded, consistent snapshot of the file and its -wal), so no temporary files
// are created and the browser's own locking is never needed.
//
// File format reference: https://www.sqlite.org/fileformat2.html
//
// Every input is treated as hostile. Sizes, page counts, depth, record length,
// overflow chains, columns and WAL frames are bounded; a corrupt file produces
// ErrCorrupt (or a more specific error), never a panic or unbounded allocation.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// Bounds on what the reader accepts.
const (
	// MaxDatabaseBytes bounds the database file read into memory.
	MaxDatabaseBytes = 512 << 20
	// MaxWALBytes bounds the write-ahead log read into memory.
	MaxWALBytes = 256 << 20

	maxTreeDepth    = 32
	maxColumns      = 4096
	maxRecordBytes  = 16 << 20
	maxSchemaBytes  = 1 << 20
	maxSchemaTables = 4096
	maxSnapshotTry  = 3
)

var (
	// ErrCorrupt reports a database or log that violates the file format.
	ErrCorrupt = errors.New("sqlite: corrupt database")
	// ErrUnsupported reports valid SQLite features this reader does not handle
	// (for example UTF-16 text, WITHOUT ROWID tables, or virtual tables).
	ErrUnsupported = errors.New("sqlite: unsupported database feature")
	// ErrNoTable reports a table that is not in the database.
	ErrNoTable = errors.New("sqlite: no such table")
)

// recovered counts panics the boundary guards converted to errors. Tests assert
// it stays zero: the guards are defense in depth, not control flow.
var recovered atomic.Int64

func corrupt(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrCorrupt}, args...)...)
}

func guard(err *error) {
	if r := recover(); r != nil {
		recovered.Add(1)
		*err = corrupt("internal error while reading")
	}
}

// work bounds what one Scan, Lookup or schema load may do, so a file whose cells
// share overflow chains or whose tables declare many columns cannot make a
// small file cost unbounded time or memory. A valid database never reaches the
// limits: its overflow pages each belong to one cell, so the bytes assembled
// from overflow pages cannot exceed the file size.
type work struct {
	overflow int64 // overflow bytes still allowed
	slots    int64 // column values still allowed
}

const maxSlots = 200 << 20

func (db *Database) newWork() *work {
	return &work{overflow: int64(len(db.data)) + int64(len(db.wal)) + 64<<10, slots: maxSlots}
}

// Value is a SQLite value: nil, int64, float64, string or []byte.
type Value any

// Row is one table row. RowID is the rowid; a column declared INTEGER PRIMARY
// KEY is an alias for it and reads as RowID.
type Row struct {
	RowID   int64
	columns []string
	values  []Value
}

// Columns returns the column names. The slice is shared; do not modify it.
func (r Row) Columns() []string { return r.columns }

// Get returns a column by name, case-insensitively.
func (r Row) Get(name string) (Value, bool) {
	for i, column := range r.columns {
		if strings.EqualFold(column, name) {
			return r.values[i], true
		}
	}
	return nil, false
}

// Int returns an INTEGER column; a REAL with an integral value is converted.
func (r Row) Int(name string) (int64, bool) {
	value, ok := r.Get(name)
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case int64:
		return v, true
	case float64:
		if v == math.Trunc(v) && v >= -(1<<63) && v < 1<<63 {
			return int64(v), true
		}
	}
	return 0, false
}

// Text returns a TEXT column. A NULL or non-text value reports false.
func (r Row) Text(name string) (string, bool) {
	value, ok := r.Get(name)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// Bytes returns a BLOB column (or the bytes of a TEXT column).
func (r Row) Bytes(name string) ([]byte, bool) {
	value, ok := r.Get(name)
	if !ok {
		return nil, false
	}
	switch v := value.(type) {
	case []byte:
		return v, true
	case string:
		return []byte(v), true
	}
	return nil, false
}

// Database is a read-only snapshot of a SQLite database and its write-ahead log.
type Database struct {
	data     []byte
	wal      []byte
	frames   map[uint32]int // page number -> offset of the page image in wal
	pageSize int
	usable   int
	pages    uint32
	tables   map[string]*tableInfo
	names    []string
}

type tableInfo struct {
	name     string
	root     uint32
	columns  []string
	aliasIdx int // index of the INTEGER PRIMARY KEY column, or -1
	types    []string
	defaults []defaultValue
	err      error // set when the table cannot be read by this reader
}

type defaultValue struct {
	known bool
	value Value
}

// Open reads a database and its -wal file into memory. The file pair is read
// again if it changes while being read, so the snapshot is consistent. Nothing
// is created or modified.
func Open(path string) (db *Database, err error) {
	defer guard(&err)
	var data, wal []byte
	for attempt := 0; attempt < maxSnapshotTry; attempt++ {
		beforeDB, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !beforeDB.Mode().IsRegular() {
			return nil, corrupt("not a regular file")
		}
		beforeWAL, walErr := os.Stat(path + "-wal")
		if walErr != nil && !errors.Is(walErr, os.ErrNotExist) {
			return nil, walErr
		}
		data, err = readBounded(path, MaxDatabaseBytes)
		if err != nil {
			return nil, err
		}
		wal = nil
		if walErr == nil {
			wal, err = readBounded(path+"-wal", MaxWALBytes)
			if errors.Is(err, os.ErrNotExist) {
				wal, err = nil, nil
			}
			if err != nil {
				return nil, err
			}
		}
		afterDB, dbErr := os.Stat(path)
		afterWAL, afterWalErr := os.Stat(path + "-wal")
		stable := dbErr == nil && sameFileVersion(beforeDB, afterDB) &&
			((walErr != nil && afterWalErr != nil) || (walErr == nil && afterWalErr == nil && sameFileVersion(beforeWAL, afterWAL)))
		if stable {
			return OpenBytes(data, wal)
		}
	}
	return nil, errors.New("sqlite: database changed while it was being read; retry after the browser is idle")
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("sqlite: %s exceeds %d MiB", baseName(path), limit>>20)
	}
	return data, nil
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// OpenBytes parses a database image and an optional write-ahead log image. wal
// may be nil or empty. The slices are not modified and are kept by the result.
func OpenBytes(data, wal []byte) (db *Database, err error) {
	defer guard(&err)
	if len(data) > MaxDatabaseBytes || len(wal) > MaxWALBytes {
		return nil, corrupt("input exceeds its size bound")
	}
	if len(data) < 100 || !bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
		return nil, corrupt("not a SQLite database")
	}
	pageSize := int(binary.BigEndian.Uint16(data[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 {
		return nil, corrupt("invalid page size")
	}
	if data[18] > 2 || data[19] > 2 {
		return nil, fmt.Errorf("%w: file format version", ErrUnsupported)
	}
	reserved := int(data[20])
	usable := pageSize - reserved
	if usable < 480 {
		return nil, corrupt("invalid reserved space")
	}
	if encoding := binary.BigEndian.Uint32(data[56:60]); encoding > 1 {
		return nil, fmt.Errorf("%w: text encoding is not UTF-8", ErrUnsupported)
	}
	db = &Database{data: data, pageSize: pageSize, usable: usable}
	pages := binary.BigEndian.Uint32(data[28:32])
	if pages == 0 || binary.BigEndian.Uint32(data[24:28]) != binary.BigEndian.Uint32(data[92:96]) {
		pages = uint32(len(data) / pageSize)
	}
	db.pages = pages
	if err := db.replay(wal); err != nil {
		return nil, err
	}
	if db.pages == 0 || db.pages > math.MaxInt32 {
		return nil, corrupt("invalid page count")
	}
	if err := db.loadSchema(); err != nil {
		return nil, err
	}
	return db, nil
}

// replay applies the committed frames of a write-ahead log, following the
// checksum chain described in the file format. An absent, empty, or invalid
// header means no log, as in SQLite; a log for another page size is an error.
func (db *Database) replay(wal []byte) error {
	if len(wal) < 32 {
		return nil
	}
	magic := binary.BigEndian.Uint32(wal[0:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		return nil
	}
	bigEndian := magic&1 == 1
	if binary.BigEndian.Uint32(wal[4:8]) != 3007000 {
		return nil
	}
	if int(binary.BigEndian.Uint32(wal[8:12])) != db.pageSize {
		return corrupt("write-ahead log page size does not match the database")
	}
	salt1, salt2 := binary.BigEndian.Uint32(wal[16:20]), binary.BigEndian.Uint32(wal[20:24])
	s0, s1 := walChecksum(wal[:24], bigEndian, 0, 0)
	if s0 != binary.BigEndian.Uint32(wal[24:28]) || s1 != binary.BigEndian.Uint32(wal[28:32]) {
		return nil
	}
	frameSize := 24 + db.pageSize
	committed := map[uint32]int{}
	pending := map[uint32]int{}
	committedPages := uint32(0)
	for offset := 32; offset+frameSize <= len(wal); offset += frameSize {
		header := wal[offset : offset+24]
		if binary.BigEndian.Uint32(header[8:12]) != salt1 || binary.BigEndian.Uint32(header[12:16]) != salt2 {
			break
		}
		page := binary.BigEndian.Uint32(header[0:4])
		if page == 0 {
			break
		}
		n0, n1 := walChecksum(header[:8], bigEndian, s0, s1)
		n0, n1 = walChecksum(wal[offset+24:offset+frameSize], bigEndian, n0, n1)
		if n0 != binary.BigEndian.Uint32(header[16:20]) || n1 != binary.BigEndian.Uint32(header[20:24]) {
			break
		}
		s0, s1 = n0, n1
		pending[page] = offset + 24
		if size := binary.BigEndian.Uint32(header[4:8]); size != 0 {
			for p, o := range pending {
				committed[p] = o
			}
			pending = map[uint32]int{}
			committedPages = size
		}
	}
	if len(committed) > 0 {
		db.wal = wal
		db.frames = committed
		db.pages = committedPages
	}
	return nil
}

func walChecksum(data []byte, bigEndian bool, s0, s1 uint32) (uint32, uint32) {
	for i := 0; i+8 <= len(data); i += 8 {
		var x0, x1 uint32
		if bigEndian {
			x0, x1 = binary.BigEndian.Uint32(data[i:]), binary.BigEndian.Uint32(data[i+4:])
		} else {
			x0, x1 = binary.LittleEndian.Uint32(data[i:]), binary.LittleEndian.Uint32(data[i+4:])
		}
		s0 += x0 + s1
		s1 += x1 + s0
	}
	return s0, s1
}

// page returns the image of a page, from the log when a committed frame holds it.
func (db *Database) page(number uint32) ([]byte, error) {
	if number == 0 || number > db.pages {
		return nil, corrupt("page number out of range")
	}
	if offset, ok := db.frames[number]; ok {
		return db.wal[offset : offset+db.pageSize], nil
	}
	start := (int64(number) - 1) * int64(db.pageSize)
	if start+int64(db.pageSize) > int64(len(db.data)) {
		return nil, corrupt("page lies beyond the end of the file")
	}
	return db.data[start : start+int64(db.pageSize)], nil
}

// UserVersion returns the user_version stored in the database header, the value
// of PRAGMA user_version.
func (db *Database) UserVersion() (version int32, err error) {
	defer guard(&err)
	first, err := db.page(1)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(first[60:64])), nil
}

// Tables returns the names of the tables in the database, in schema order.
func (db *Database) Tables() []string { return append([]string(nil), db.names...) }

// Columns returns a table's column names. Names compare case-insensitively.
func (db *Database) Columns(table string) ([]string, error) {
	info, err := db.table(table)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), info.columns...), nil
}

func (db *Database) table(name string) (*tableInfo, error) {
	info, ok := db.tables[strings.ToLower(name)]
	if !ok {
		return nil, ErrNoTable
	}
	if info.err != nil {
		return nil, info.err
	}
	return info, nil
}

// Scan calls fn for every row of a table in rowid order. fn returning an error
// stops the scan with that error. The Row and its values are only valid until
// the next call; copy anything kept.
func (db *Database) Scan(table string, fn func(Row) error) (err error) {
	defer guard(&err)
	info, err := db.table(table)
	if err != nil {
		return err
	}
	visited := map[uint32]bool{}
	budget := db.newWork()
	return db.walk(info, info.root, 0, visited, budget, func(rowid int64, payload []byte) error {
		row, err := db.decodeRow(info, rowid, payload, budget)
		if err != nil {
			return err
		}
		return fn(row)
	})
}

// Lookup returns the row with the given rowid, or ok false when there is none.
func (db *Database) Lookup(table string, rowid int64) (row Row, ok bool, err error) {
	defer guard(&err)
	info, err := db.table(table)
	if err != nil {
		return Row{}, false, err
	}
	number := info.root
	visited := map[uint32]bool{}
	budget := db.newWork()
	for depth := 0; depth <= maxTreeDepth; depth++ {
		if visited[number] || (number == 1 && info.root != 1) {
			return Row{}, false, corrupt("b-tree page visited twice or reused")
		}
		visited[number] = true
		page, header, err := db.treePage(number)
		if err != nil {
			return Row{}, false, err
		}
		cells, err := db.cellOffsets(number, page, header)
		if err != nil {
			return Row{}, false, err
		}
		if header.leaf {
			for _, offset := range cells {
				_, key, payload, err := db.leafCell(page, offset, budget)
				if err != nil {
					return Row{}, false, err
				}
				if key == rowid {
					row, err := db.decodeRow(info, key, payload, budget)
					return row, err == nil, err
				}
				if key > rowid {
					break
				}
			}
			return Row{}, false, nil
		}
		next := header.rightmost
		for _, offset := range cells {
			child, key, err := interiorCell(page, offset)
			if err != nil {
				return Row{}, false, err
			}
			if rowid <= key {
				next = child
				break
			}
		}
		number = next
	}
	return Row{}, false, corrupt("b-tree is too deep")
}

type treeHeader struct {
	leaf      bool
	cells     int
	offset    int // start of the page header (100 on page 1)
	rightmost uint32
}

func (db *Database) treePage(number uint32) ([]byte, treeHeader, error) {
	page, err := db.page(number)
	if err != nil {
		return nil, treeHeader{}, err
	}
	offset := 0
	if number == 1 {
		offset = 100
	}
	var header treeHeader
	header.offset = offset
	switch page[offset] {
	case 0x0d:
		header.leaf = true
	case 0x05:
		header.rightmost = binary.BigEndian.Uint32(page[offset+8 : offset+12])
	case 0x0a, 0x02:
		return nil, header, fmt.Errorf("%w: expected a table b-tree page", ErrUnsupported)
	default:
		return nil, header, corrupt("unknown b-tree page type")
	}
	header.cells = int(binary.BigEndian.Uint16(page[offset+3 : offset+5]))
	return page, header, nil
}

// cellOffsets validates the cell pointer array of a page.
func (db *Database) cellOffsets(number uint32, page []byte, header treeHeader) ([]int, error) {
	headerSize := 8
	if !header.leaf {
		headerSize = 12
	}
	arrayStart := header.offset + headerSize
	arrayEnd := arrayStart + 2*header.cells
	if arrayEnd > db.usable {
		return nil, corrupt("cell pointer array overflows its page")
	}
	cells := make([]int, header.cells)
	for i := range cells {
		offset := int(binary.BigEndian.Uint16(page[arrayStart+2*i:]))
		if offset < arrayEnd || offset >= db.usable {
			return nil, corrupt("cell offset out of range")
		}
		cells[i] = offset
	}
	return cells, nil
}

// walk visits the leaf cells of a table b-tree in order.
func (db *Database) walk(info *tableInfo, number uint32, depth int, visited map[uint32]bool, budget *work, fn func(rowid int64, payload []byte) error) error {
	if depth > maxTreeDepth {
		return corrupt("b-tree is too deep")
	}
	if visited[number] {
		return corrupt("b-tree page visited twice")
	}
	if number == 1 && info.root != 1 {
		return corrupt("a table b-tree may not use the schema page")
	}
	visited[number] = true
	page, header, err := db.treePage(number)
	if err != nil {
		return err
	}
	cells, err := db.cellOffsets(number, page, header)
	if err != nil {
		return err
	}
	if header.leaf {
		for _, offset := range cells {
			_, rowid, payload, err := db.leafCell(page, offset, budget)
			if err != nil {
				return err
			}
			if err := fn(rowid, payload); err != nil {
				return err
			}
		}
		return nil
	}
	for _, offset := range cells {
		child, _, err := interiorCell(page, offset)
		if err != nil {
			return err
		}
		if err := db.walk(info, child, depth+1, visited, budget, fn); err != nil {
			return err
		}
	}
	return db.walk(info, header.rightmost, depth+1, visited, budget, fn)
}

func interiorCell(page []byte, offset int) (child uint32, key int64, err error) {
	if offset+4 >= len(page) {
		return 0, 0, corrupt("interior cell is truncated")
	}
	child = binary.BigEndian.Uint32(page[offset:])
	value, n := varint(page[offset+4:])
	if n == 0 {
		return 0, 0, corrupt("interior cell key is truncated")
	}
	return child, int64(value), nil
}

// leafCell parses a table leaf cell and assembles its payload, following the
// overflow chain when the payload does not fit on the page.
func (db *Database) leafCell(page []byte, offset int, budget *work) (size uint64, rowid int64, payload []byte, err error) {
	size, n := varint(page[offset:])
	if n == 0 {
		return 0, 0, nil, corrupt("cell size is truncated")
	}
	offset += n
	key, n := varint(page[offset:])
	if n == 0 {
		return 0, 0, nil, corrupt("cell rowid is truncated")
	}
	offset += n
	if size > maxRecordBytes {
		return 0, 0, nil, corrupt("record exceeds its size bound")
	}
	total := int(size)
	local := db.localPayload(total)
	if offset+local > db.usable {
		return 0, 0, nil, corrupt("cell payload overflows its page")
	}
	if local == total {
		return size, int64(key), page[offset : offset+local], nil
	}
	if offset+local+4 > db.usable {
		return 0, 0, nil, corrupt("overflow pointer is truncated")
	}
	// The payload grows only as overflow pages are actually read, never to the
	// size the cell claims, and a page may appear in a chain only once.
	assembled := append([]byte(nil), page[offset:offset+local]...)
	next := binary.BigEndian.Uint32(page[offset+local:])
	chunk := db.usable - 4
	seen := map[uint32]bool{}
	for len(assembled) < total {
		if next == 0 || seen[next] || len(seen) > total/chunk+1 {
			return 0, 0, nil, corrupt("overflow chain is invalid")
		}
		seen[next] = true
		overflow, err := db.page(next)
		if err != nil {
			return 0, 0, nil, err
		}
		take := total - len(assembled)
		if take > chunk {
			take = chunk
		}
		if budget.overflow -= int64(take); budget.overflow < 0 {
			return 0, 0, nil, corrupt("overflow pages are read more than once")
		}
		assembled = append(assembled, overflow[4:4+take]...)
		next = binary.BigEndian.Uint32(overflow[0:4])
	}
	return size, int64(key), assembled, nil
}

// localPayload is how many bytes of a payload stay on a table leaf page.
func (db *Database) localPayload(total int) int {
	u := db.usable
	maxLocal := u - 35
	if total <= maxLocal {
		return total
	}
	minLocal := (u-12)*32/255 - 23
	local := minLocal + (total-minLocal)%(u-4)
	if local <= maxLocal {
		return local
	}
	return minLocal
}

// varint reads a SQLite varint; n is 0 when the data is too short.
func varint(data []byte) (value uint64, n int) {
	for i := 0; i < 8; i++ {
		if i >= len(data) {
			return 0, 0
		}
		value = value<<7 | uint64(data[i]&0x7f)
		if data[i]&0x80 == 0 {
			return value, i + 1
		}
	}
	if len(data) < 9 {
		return 0, 0
	}
	return value<<8 | uint64(data[8]), 9
}

// decodeRow decodes a record into the table's columns. Missing trailing columns
// take their declared default.
func (db *Database) decodeRow(info *tableInfo, rowid int64, payload []byte, budget *work) (Row, error) {
	headerSize, n := varint(payload)
	if n == 0 || headerSize < uint64(n) || headerSize > uint64(len(payload)) {
		return Row{}, corrupt("record header is invalid")
	}
	header := payload[n:headerSize]
	body := payload[headerSize:]
	if budget.slots -= int64(len(info.columns)); budget.slots < 0 {
		return Row{}, corrupt("table is too large to read")
	}
	values := make([]Value, len(info.columns))
	column := 0
	for len(header) > 0 {
		serial, used := varint(header)
		if used == 0 {
			return Row{}, corrupt("record serial type is truncated")
		}
		header = header[used:]
		if column >= maxColumns {
			return Row{}, corrupt("record has too many columns")
		}
		size, err := serialSize(serial)
		if err != nil {
			return Row{}, err
		}
		if uint64(len(body)) < size {
			return Row{}, corrupt("record body is truncated")
		}
		value, err := decodeValue(serial, body[:size])
		if err != nil {
			return Row{}, err
		}
		body = body[size:]
		if column < len(values) {
			values[column] = value
		}
		column++
	}
	for ; column < len(values); column++ {
		if def := info.defaults[column]; def.known {
			values[column] = def.value
		} else {
			return Row{}, fmt.Errorf("%w: column default", ErrUnsupported)
		}
	}
	if info.aliasIdx >= 0 {
		values[info.aliasIdx] = rowid
	}
	return Row{RowID: rowid, columns: info.columns, values: values}, nil
}

func serialSize(serial uint64) (uint64, error) {
	switch {
	case serial <= 4:
		return serial, nil // 0 is NULL; 1 to 4 are 1 to 4 byte integers
	case serial == 5:
		return 6, nil
	case serial == 6 || serial == 7:
		return 8, nil
	case serial == 8 || serial == 9:
		return 0, nil
	case serial == 10 || serial == 11:
		return 0, corrupt("reserved serial type")
	}
	size := (serial - 12) / 2
	if size > maxRecordBytes {
		return 0, corrupt("value exceeds its size bound")
	}
	return size, nil
}

func decodeValue(serial uint64, data []byte) (Value, error) {
	switch {
	case serial == 0:
		return nil, nil
	case serial >= 1 && serial <= 6:
		var v int64
		for _, b := range data {
			v = v<<8 | int64(b)
		}
		if len(data) > 0 && data[0]&0x80 != 0 { // sign-extend
			v -= int64(1) << (8 * uint(len(data)))
		}
		return v, nil
	case serial == 7:
		return math.Float64frombits(binary.BigEndian.Uint64(data)), nil
	case serial == 8:
		return int64(0), nil
	case serial == 9:
		return int64(1), nil
	case serial%2 == 1:
		if !utf8.Valid(data) {
			return nil, corrupt("text value is not valid UTF-8")
		}
		return string(data), nil
	default:
		return append([]byte(nil), data...), nil
	}
}

// OpenShared is Open with a small cache: repeated opens of the same unchanged
// file pair (database and -wal, compared by size and modification time) return
// the same read-only snapshot, so a caller that reads many rows one at a time,
// such as a per-cookie value lookup, does not read the file again each time.
// The cache holds at most a few entries and is safe for concurrent use.
func OpenShared(path string) (*Database, error) {
	key, ok := sharedKey(path)
	if ok {
		sharedMu.Lock()
		cached := sharedCache[path]
		sharedMu.Unlock()
		if cached != nil && cached.key == key {
			return cached.db, nil
		}
	}
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	// Key again after reading: if the files moved on, do not cache the snapshot.
	if after, stable := sharedKey(path); ok && stable && after == key {
		sharedMu.Lock()
		if len(sharedCache) >= 4 {
			for other := range sharedCache {
				delete(sharedCache, other)
				break
			}
		}
		sharedCache[path] = &sharedEntry{key: key, db: db}
		sharedMu.Unlock()
	}
	return db, nil
}

type sharedEntry struct {
	key string
	db  *Database
}

var (
	sharedMu    sync.Mutex
	sharedCache = map[string]*sharedEntry{}
)

func sharedKey(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	key := fmt.Sprintf("%d/%d", info.Size(), info.ModTime().UnixNano())
	if walInfo, err := os.Stat(path + "-wal"); err == nil {
		key += fmt.Sprintf("|%d/%d", walInfo.Size(), walInfo.ModTime().UnixNano())
	}
	return key, true
}
