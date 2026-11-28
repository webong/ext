#!/bin/sh
# Regenerates the synthetic SQLite fixtures in this directory with the sqlite3
# command-line shell. The data is invented (no real cookies). Needs sqlite3 3.31
# or newer. The committed files were made with 3.50.4.
set -eu
cd "$(dirname "$0")"
rm -f *.db *.db-wal *.db-shm

# A Chromium-shaped store: cookies plus a meta table whose UNIQUE PRIMARY KEY
# creates an index (index pages must never be walked as tables).
sqlite3 chromium.db <<'SQL'
CREATE TABLE meta(key LONGVARCHAR NOT NULL UNIQUE PRIMARY KEY, value LONGVARCHAR);
INSERT INTO meta VALUES('version','24'),('last_compatible_version','24');
CREATE TABLE cookies(creation_utc INTEGER NOT NULL, host_key TEXT NOT NULL, top_frame_site_key TEXT NOT NULL,
  name TEXT NOT NULL, value TEXT NOT NULL, encrypted_value BLOB NOT NULL, path TEXT NOT NULL,
  expires_utc INTEGER NOT NULL, is_secure INTEGER NOT NULL, is_httponly INTEGER NOT NULL,
  last_access_utc INTEGER NOT NULL, has_expires INTEGER NOT NULL, is_persistent INTEGER NOT NULL,
  priority INTEGER NOT NULL, samesite INTEGER NOT NULL, source_scheme INTEGER NOT NULL,
  source_port INTEGER NOT NULL, last_update_utc INTEGER NOT NULL, source_type INTEGER NOT NULL,
  has_cross_site_ancestor INTEGER NOT NULL);
CREATE UNIQUE INDEX cookies_unique_index ON cookies(host_key, top_frame_site_key, has_cross_site_ancestor, name, path, source_scheme, source_port);
INSERT INTO cookies VALUES
 (13300000000000000,'example.com','','sid','plain-value',x'','/',14500000000000000,1,1,13300000000000001,1,1,1,-1,2,443,13300000000000002,0,0),
 (13300000000000010,'.example.com','','pref','',CAST(x'763130' || x'aabbccddeeff00112233445566778899' AS BLOB),'/app',0,0,0,13300000000000011,0,0,1,0,1,80,13300000000000012,0,0),
 (13300000000000020,'.sub.example.com','https://top.example','tracking','',x'763131aa','/',14500000000000100,1,0,13300000000000021,1,1,1,2,2,443,13300000000000022,0,1),
 (13300000000000030,'example.org','','unicode','caf' || char(233) || ' ' || char(9731),x'','/p q',14500000000000200,0,0,13300000000000031,1,1,1,1,0,-1,13300000000000032,0,0),
 (13300000000000040,'example.org','','negative','neg',x'','/',-5,0,0,0,0,0,1,0,0,0,0,0,0),
 (9223372036854775807,'big.example','','huge','',x'','/',9007199254740993,0,0,0,1,1,1,0,0,65535,0,0,0);
SQL

# A Firefox-shaped store: id is INTEGER PRIMARY KEY (an alias of the rowid, so
# its value is not stored in the record). Rowids have gaps.
sqlite3 firefox.db <<'SQL'
PRAGMA user_version = 16;
CREATE TABLE moz_cookies(id INTEGER PRIMARY KEY, originAttributes TEXT NOT NULL DEFAULT '', name TEXT, value TEXT,
  host TEXT, path TEXT, expiry INTEGER, lastAccessed INTEGER, creationTime INTEGER, isSecure INTEGER,
  isHttpOnly INTEGER, inBrowserElement INTEGER DEFAULT 0, sameSite INTEGER DEFAULT 0, schemeMap INTEGER DEFAULT 0,
  isPartitionedAttributeSet INTEGER DEFAULT 0, updateTime INTEGER,
  CONSTRAINT moz_uniqueid UNIQUE (name, host, path, originAttributes));
INSERT INTO moz_cookies VALUES
 (1,'','sid','firefox-value','example.com','/',1893456000000,1700000000000000,1700000000000000,1,1,0,1,2,0,1700000000000000),
 (4,'^userContextId=2','container','c-value','.example.com','/',1893456001000,1700000000000001,1700000000000001,0,0,0,0,1,0,1700000000000001),
 (9,'^partitionKey=%28https%2Ctop.example%29','part','p-value','.example.net','/x',0,1700000000000002,1700000000000002,1,0,0,2,2,1,1700000000000002);
SQL

# Many pages and three b-tree levels: 512-byte pages hold about 13 of these rows.
sqlite3 multipage.db <<'SQL'
PRAGMA page_size = 512;
CREATE TABLE cookies(id INTEGER PRIMARY KEY, host_key TEXT NOT NULL, name TEXT NOT NULL, value TEXT NOT NULL);
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2500)
INSERT INTO cookies SELECT i, 'h' || (i % 7) || '.example', 'c' || i, 'v' || (i * 31 % 997) FROM n;
SQL

# Payloads around the on-page limit and far beyond it (overflow chains). With
# 1024-byte pages a table leaf keeps at most 989 payload bytes locally.
sqlite3 overflow.db <<'SQL'
PRAGMA page_size = 1024;
CREATE TABLE blobs(id INTEGER PRIMARY KEY, label TEXT, data, note TEXT);
-- A repeating 16-character pattern shifts against the page size, so overflow
-- pages read in the wrong order or at the wrong offset change the content.
WITH RECURSIVE sizes(id, label, size) AS (VALUES
  (2,'just-under',940),(3,'at-limit',960),(4,'just-over',1000),(5,'two-pages',1700),
  (6,'ten-pages',10000),(7,'big',30000))
INSERT INTO blobs SELECT id, label, substr(replace(hex(zeroblob((size + 1) / 2)), '00', '0123456789abcdef'), 1, size), 'tail' FROM sizes;
INSERT INTO blobs VALUES (1,'small',x'0102',NULL), (8,'blob-ten-pages',CAST(substr(replace(hex(zeroblob(5000)), '00', 'ABCDEFGHIJKLMNOP'), 1, 10000) AS BLOB),'tail');
SQL

# A write-ahead-log database with commits that exist only in the -wal file: the
# connection stays open (and checkpointing is off) while the files are copied.
rm -rf .walwork && mkdir .walwork
sqlite3 .walwork/source.db <<'SQL'
PRAGMA journal_mode = WAL;
PRAGMA wal_autocheckpoint = 0;
CREATE TABLE cookies(id INTEGER PRIMARY KEY, name TEXT, value TEXT);
INSERT INTO cookies VALUES (1,'one','from-main-file'),(2,'two','from-main-file');
PRAGMA wal_checkpoint(TRUNCATE);
INSERT INTO cookies VALUES (3,'three','from-wal'),(4,'four','from-wal');
UPDATE cookies SET value = 'updated-in-wal' WHERE id = 2;
DELETE FROM cookies WHERE id = 1;
.shell cp .walwork/source.db wal.db
.shell cp .walwork/source.db-wal wal.db-wal
SQL
rm -rf .walwork

# ALTER TABLE ADD COLUMN: rows written before the change carry no record bytes
# for the new columns and read as their declared defaults.
sqlite3 altered.db <<'SQL'
CREATE TABLE cookies(id INTEGER PRIMARY KEY, name TEXT NOT NULL);
INSERT INTO cookies VALUES (1,'old-a'),(2,'old-b');
ALTER TABLE cookies ADD COLUMN level INTEGER NOT NULL DEFAULT 7;
ALTER TABLE cookies ADD COLUMN note TEXT DEFAULT 'n/a';
ALTER TABLE cookies ADD COLUMN ratio REAL DEFAULT -1.5;
ALTER TABLE cookies ADD COLUMN extra INTEGER;
INSERT INTO cookies VALUES (3,'new',1,'set',2.5,9);
SQL

# Features this reader does not handle must be reported, not misread.
sqlite3 withoutrowid.db <<'SQL'
CREATE TABLE cookies(name TEXT PRIMARY KEY, value TEXT) WITHOUT ROWID;
INSERT INTO cookies VALUES ('a','b');
SQL
sqlite3 utf16.db <<'SQL'
PRAGMA encoding = 'UTF-16le';
CREATE TABLE cookies(id INTEGER PRIMARY KEY, name TEXT);
INSERT INTO cookies VALUES (1,'x');
SQL
sqlite3 generated.db <<'SQL'
CREATE TABLE cookies(id INTEGER PRIMARY KEY, a INTEGER, b INTEGER GENERATED ALWAYS AS (a + 1) VIRTUAL);
INSERT INTO cookies(id, a) VALUES (1, 41);
SQL
echo "fixtures regenerated"
