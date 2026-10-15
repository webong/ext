package sqlite

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

// largeCookies builds a realistic large Chromium-shaped store (about 50 MB, 200 000
// cookies with encrypted values) with the sqlite3 shell. The benchmarks skip
// without it.
func largeCookies(b *testing.B) string {
	b.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		b.Skip("sqlite3 is not installed")
	}
	path := filepath.Join(b.TempDir(), "Cookies")
	script := `
CREATE TABLE cookies(creation_utc INTEGER NOT NULL, host_key TEXT NOT NULL, top_frame_site_key TEXT NOT NULL,
  name TEXT NOT NULL, value TEXT NOT NULL, encrypted_value BLOB NOT NULL, path TEXT NOT NULL,
  expires_utc INTEGER NOT NULL, is_secure INTEGER NOT NULL, is_httponly INTEGER NOT NULL,
  last_access_utc INTEGER NOT NULL, has_expires INTEGER NOT NULL, is_persistent INTEGER NOT NULL,
  priority INTEGER NOT NULL, samesite INTEGER NOT NULL, source_scheme INTEGER NOT NULL,
  source_port INTEGER NOT NULL, last_update_utc INTEGER NOT NULL, source_type INTEGER NOT NULL,
  has_cross_site_ancestor INTEGER NOT NULL);
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 200000)
INSERT INTO cookies SELECT 13300000000000000 + i, 'host' || (i % 5000) || '.example.com', '', 'cookie' || i, '',
  CAST('v10' || hex(zeroblob(80 + i % 60)) AS BLOB), '/', 14500000000000000, 1, 1, 13300000000000000 + i, 1, 1, 1, -1, 2, 443,
  13300000000000000 + i, 0, 0 FROM n;`
	command := exec.Command("sqlite3", path)
	command.Stdin = bytes.NewBufferString(script)
	if out, err := command.CombinedOutput(); err != nil {
		b.Fatalf("%v %s", err, out)
	}
	return path
}

func BenchmarkOpenAndScanLargeCookies(b *testing.B) {
	path := largeCookies(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db, err := Open(path)
		if err != nil {
			b.Fatal(err)
		}
		rows := 0
		if err := db.Scan("cookies", func(r Row) error { rows++; return nil }); err != nil {
			b.Fatal(err)
		}
		if rows != 200000 {
			b.Fatalf("%d rows", rows)
		}
	}
}

func BenchmarkLookupLargeCookies(b *testing.B) {
	path := largeCookies(b)
	db, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok, err := db.Lookup("cookies", int64(1+i*7919%200000)); err != nil || !ok {
			b.Fatal(err)
		}
	}
}

// For comparison: the same full read through the sqlite3 shell, as the cookie
// readers did before they used the built-in reader.
func BenchmarkShellJSONLargeCookies(b *testing.B) {
	path := largeCookies(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := exec.Command("sqlite3", "-readonly", "-json", path, "SELECT rowid AS id,name,host_key AS host,path,expires_utc,is_secure,is_httponly,samesite,top_frame_site_key,has_cross_site_ancestor,has_expires FROM cookies").Output()
		if err != nil || len(out) < 1000 {
			b.Fatal(err)
		}
	}
}
