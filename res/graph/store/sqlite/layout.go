package sqlite

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// tables.json is a copy of res/graph/testdata/store/v1/tables.json, the
// declarative layout the contract fixes. TestLayoutMatchesContract fails when
// the copy drifts; refresh it with `go generate`.
//
//go:generate cp ../../testdata/store/v1/tables.json tables.json
//go:embed tables.json
var tablesJSON []byte

type layout struct {
	Version int    `json:"version"`
	Layout  string `json:"layout"`
	Pragmas struct {
		JournalMode   string `json:"journal_mode"`
		ForeignKeys   string `json:"foreign_keys"`
		BusyTimeoutMS int    `json:"busy_timeout_ms"`
	} `json:"pragmas"`
	Tables  []table `json:"tables"`
	Indexes []index `json:"indexes"`
}

type table struct {
	Name        string       `json:"name"`
	Columns     []column     `json:"columns"`
	PrimaryKey  []string     `json:"primary_key"`
	ForeignKeys []foreignKey `json:"foreign_keys"`
	WithoutRow  bool         `json:"without_rowid"`
}

type column struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"not_null"`
	Check   string `json:"check"`
}

type foreignKey struct {
	Columns    []string `json:"columns"`
	References string   `json:"references"`
	To         []string `json:"to"`
}

type index struct {
	Name    string   `json:"name"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
}

func loadLayout() (layout, error) {
	var l layout
	if err := json.Unmarshal(tablesJSON, &l); err != nil {
		return layout{}, fmt.Errorf("graph store layout: %w", err)
	}
	if l.Version != 1 || l.Layout == "" || len(l.Tables) == 0 {
		return layout{}, fmt.Errorf("graph store layout: unexpected version %d", l.Version)
	}
	return l, nil
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func quoteAll(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = quote(n)
	}
	return strings.Join(out, ", ")
}

// statements returns the CREATE statements of the layout, tables first.
func (l layout) statements() []string {
	var out []string
	for _, t := range l.Tables {
		var parts []string
		for _, c := range t.Columns {
			def := quote(c.Name) + " " + c.Type
			if c.NotNull {
				def += " NOT NULL"
			}
			if c.Check != "" {
				def += " CHECK (" + c.Check + ")"
			}
			parts = append(parts, def)
		}
		if len(t.PrimaryKey) > 0 {
			parts = append(parts, "PRIMARY KEY ("+quoteAll(t.PrimaryKey)+")")
		}
		for _, fk := range t.ForeignKeys {
			parts = append(parts, "FOREIGN KEY ("+quoteAll(fk.Columns)+") REFERENCES "+quote(fk.References)+" ("+quoteAll(fk.To)+")")
		}
		stmt := "CREATE TABLE " + quote(t.Name) + " (\n  " + strings.Join(parts, ",\n  ") + "\n)"
		if t.WithoutRow {
			stmt += " WITHOUT ROWID"
		}
		out = append(out, stmt)
	}
	for _, ix := range l.Indexes {
		out = append(out, "CREATE INDEX "+quote(ix.Name)+" ON "+quote(ix.Table)+" ("+quoteAll(ix.Columns)+")")
	}
	return out
}
