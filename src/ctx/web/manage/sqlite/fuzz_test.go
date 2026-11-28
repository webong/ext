package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzReader feeds arbitrary database and log images to every public entry point.
// Run it with:
//
//	go test -run '^$' -fuzz FuzzReader -fuzztime 120s -fuzzminimizetime 0 ./sqlite
//
// -fuzzminimizetime 0 matters: the seeds are database images of up to 77 KB, and
// by default the Go fuzz engine spends up to 60 s minimizing each new input it
// finds, during which the exec counter reads 0/sec. That is the engine working,
// not the reader hanging (TestNoInputIsSlow bounds the reader's time per input).
func FuzzReader(f *testing.F) {
	for _, name := range []string{"chromium.db", "firefox.db", "multipage.db", "overflow.db", "altered.db", "withoutrowid.db", "generated.db", "utf16.db"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data, []byte(nil))
	}
	database, _ := os.ReadFile(filepath.Join("testdata", "wal.db"))
	wal, _ := os.ReadFile(filepath.Join("testdata", "wal.db-wal"))
	f.Add(database, wal)
	f.Add([]byte("SQLite format 3\x00"), []byte{})
	f.Fuzz(func(t *testing.T, data, log []byte) {
		before := recovered.Load()
		db, err := OpenBytes(data, log)
		if err != nil {
			if recovered.Load() != before {
				t.Fatal("open recovered from an internal panic")
			}
			return
		}
		for _, table := range db.Tables() {
			rows := 0
			_ = db.Scan(table, func(Row) error {
				rows++
				if rows > 200000 {
					return os.ErrDeadlineExceeded
				}
				return nil
			})
			for _, id := range []int64{0, 1, 3, 1 << 33} {
				_, _, _ = db.Lookup(table, id)
			}
			_, _ = db.Columns(table)
		}
		_, _ = db.UserVersion()
		if recovered.Load() != before {
			t.Fatal("scan recovered from an internal panic")
		}
	})
}
