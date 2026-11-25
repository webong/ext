package graphapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	model "github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/res/system/service/host/dirlock"
	_ "modernc.org/sqlite"
)

// Migration is a snapshot into a new file store. The SQLite database and its
// history are retained unchanged; cursors belong to the new store generation.
// A private staging directory is published only after every namespace succeeds.
func ensureStore(ctx context.Context, lib *model.Library, home string) error {
	target := filepath.Join(home, Directory)
	if _, err := os.Lstat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	release, err := dirlock.Acquire(filepath.Join(home, ".ledger-migration.lock"))
	if err != nil {
		return err
	}
	defer release()
	if _, err := os.Lstat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(home, ".ledger-migration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	s, err := lib.OpenStore(ctx, "file", stage, model.Options{MaxTransactionRecords: 1 << 20})
	if err != nil {
		return err
	}
	legacy := filepath.Join(home, "graph.db")
	if info, e := os.Lstat(legacy); e == nil {
		if !info.Mode().IsRegular() {
			s.Close()
			return fmt.Errorf("legacy graph is not a regular file: %s", legacy)
		}
		err = migrateSQLite(ctx, s, legacy)
	} else if !os.IsNotExist(e) {
		err = e
	}
	err = errors.Join(err, s.Close())
	if err != nil {
		return err
	}
	return os.Rename(stage, target)
}
func migrateSQLite(ctx context.Context, s *model.NativeStore, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var layout string
	if err = tx.QueryRowContext(ctx, "SELECT layout FROM store WHERE id=1").Scan(&layout); err != nil {
		return err
	}
	if layout != "ext.graph.store/v1" {
		return fmt.Errorf("unsupported legacy graph layout %q", layout)
	}
	rows, err := tx.QueryContext(ctx, "SELECT schema FROM namespaces ORDER BY namespace")
	if err != nil {
		return err
	}
	var schemas []model.Schema
	for rows.Next() {
		var text string
		var schema model.Schema
		if err = rows.Scan(&text); err == nil {
			err = json.Unmarshal([]byte(text), &schema)
		}
		if err != nil {
			rows.Close()
			return err
		}
		schemas = append(schemas, schema)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	for _, schema := range schemas {
		if err = s.Register(ctx, schema); err != nil {
			return err
		}
		desired := model.Transaction{Namespace: schema.Namespace}
		rows, err = tx.QueryContext(ctx, "SELECT id,kind,labels,attributes,provenance FROM vertices WHERE namespace=? ORDER BY id", schema.Namespace)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v model.Vertex
			var labels, attrs, prov sql.NullString
			if err = rows.Scan(&v.ID, &v.Kind, &labels, &attrs, &prov); err == nil {
				err = decodeFields(labels, attrs, prov, &v.Labels, &v.Attributes, &v.Provenance)
			}
			if err != nil {
				rows.Close()
				return err
			}
			desired.Vertices = append(desired.Vertices, v)
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, "SELECT id,type,from_id,to_id,labels,attributes,provenance FROM edges WHERE namespace=? ORDER BY id", schema.Namespace)
		if err != nil {
			return err
		}
		for rows.Next() {
			var e model.Edge
			var labels, attrs, prov sql.NullString
			if err = rows.Scan(&e.ID, &e.Type, &e.From, &e.To, &labels, &attrs, &prov); err == nil {
				err = decodeFields(labels, attrs, prov, &e.Labels, &e.Attributes, &e.Provenance)
			}
			if err != nil {
				rows.Close()
				return err
			}
			desired.Edges = append(desired.Edges, e)
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return err
		}
		if _, err = s.Apply(ctx, desired); err != nil {
			return fmt.Errorf("migrate namespace %s: %w", schema.Namespace, err)
		}
	}
	return tx.Commit()
}
func decodeFields(labels, attrs, prov sql.NullString, l, a, p any) error {
	for _, f := range []struct {
		value sql.NullString
		out   any
	}{{labels, l}, {attrs, a}, {prov, p}} {
		if f.value.Valid {
			if err := json.Unmarshal([]byte(f.value.String), f.out); err != nil {
				return err
			}
		}
	}
	return nil
}
