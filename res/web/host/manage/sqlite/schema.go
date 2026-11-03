package sqlite

import (
	"fmt"
	"strconv"
	"strings"
)

// loadSchema reads sqlite_schema (the b-tree on page 1) and parses the column
// list of each table's CREATE statement.
func (db *Database) loadSchema() error {
	schema := &tableInfo{
		name: "sqlite_schema", root: 1, aliasIdx: -1,
		columns:  []string{"type", "name", "tbl_name", "rootpage", "sql"},
		defaults: make([]defaultValue, 5),
	}
	for i := range schema.defaults {
		schema.defaults[i] = defaultValue{known: true}
	}
	db.tables = map[string]*tableInfo{}
	visited := map[uint32]bool{}
	budget := db.newWork()
	return db.walk(schema, 1, 0, visited, budget, func(rowid int64, payload []byte) error {
		row, err := db.decodeRow(schema, rowid, payload, budget)
		if err != nil {
			return err
		}
		if kind, _ := row.Text("type"); kind != "table" {
			return nil
		}
		name, _ := row.Text("name")
		root, _ := row.Int("rootpage")
		statement, _ := row.Text("sql")
		if len(db.names) >= maxSchemaTables {
			return corrupt("too many tables")
		}
		if len(statement) > maxSchemaBytes {
			return corrupt("schema statement exceeds its size bound")
		}
		info := &tableInfo{name: name, aliasIdx: -1}
		if root <= 0 || root > int64(db.pages) {
			info.err = fmt.Errorf("%w: table %q has no b-tree (virtual table)", ErrUnsupported, name)
		} else {
			info.root = uint32(root)
			if err := parseCreateTable(statement, info); err != nil {
				info.err = err
			}
		}
		key := strings.ToLower(name)
		if _, duplicate := db.tables[key]; !duplicate {
			db.names = append(db.names, name)
		}
		db.tables[key] = info
		return nil
	})
}

type token struct {
	kind byte // 'w' word, 'q' quoted identifier, 's' string, 'n' number, 'p' punctuation
	text string
}

// tokenize splits SQL into words, quoted identifiers, strings, numbers and
// punctuation, skipping whitespace and comments.
func tokenize(sql string) ([]token, error) {
	var tokens []token
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return nil, corrupt("unterminated comment in schema")
			}
			i += end + 4
		case c == '\'' || c == '"' || c == '`':
			text, next, ok := quoted(sql, i, c)
			if !ok {
				return nil, corrupt("unterminated quoted text in schema")
			}
			kind := byte('q')
			if c == '\'' {
				kind = 's'
			}
			tokens = append(tokens, token{kind, text})
			i = next
		case c == '[':
			end := strings.IndexByte(sql[i:], ']')
			if end < 0 {
				return nil, corrupt("unterminated bracket name in schema")
			}
			tokens = append(tokens, token{'q', sql[i+1 : i+end]})
			i += end + 1
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9':
			j := i
			for j < len(sql) && (sql[j] >= '0' && sql[j] <= '9' || sql[j] == '.' || sql[j] == 'e' || sql[j] == 'E' || sql[j] == 'x' || sql[j] == 'X' || (sql[j] >= 'a' && sql[j] <= 'f') || (sql[j] >= 'A' && sql[j] <= 'F') || ((sql[j] == '+' || sql[j] == '-') && (sql[j-1] == 'e' || sql[j-1] == 'E'))) {
				j++
			}
			tokens = append(tokens, token{'n', sql[i:j]})
			i = j
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80:
			j := i
			for j < len(sql) && (sql[j] == '_' || sql[j] == '$' || sql[j] >= 'a' && sql[j] <= 'z' || sql[j] >= 'A' && sql[j] <= 'Z' || sql[j] >= '0' && sql[j] <= '9' || sql[j] >= 0x80) {
				j++
			}
			tokens = append(tokens, token{'w', sql[i:j]})
			i = j
		default:
			tokens = append(tokens, token{'p', string(c)})
			i++
		}
		if len(tokens) > 1<<16 {
			return nil, corrupt("schema statement has too many tokens")
		}
	}
	return tokens, nil
}

func quoted(sql string, start int, quote byte) (text string, next int, ok bool) {
	var b strings.Builder
	for i := start + 1; i < len(sql); i++ {
		if sql[i] == quote {
			if i+1 < len(sql) && sql[i+1] == quote {
				b.WriteByte(quote)
				i++
				continue
			}
			return b.String(), i + 1, true
		}
		b.WriteByte(sql[i])
	}
	return "", 0, false
}

func isWord(t token, word string) bool { return t.kind == 'w' && strings.EqualFold(t.text, word) }

// parseCreateTable fills in a table's column names, declared defaults, and the
// column that aliases the rowid.
func parseCreateTable(statement string, info *tableInfo) error {
	tokens, err := tokenize(statement)
	if err != nil {
		return err
	}
	open := -1
	for i, t := range tokens {
		if t.kind == 'p' && t.text == "(" {
			open = i
			break
		}
	}
	if open < 0 || len(tokens) < 2 || !isWord(tokens[0], "CREATE") {
		return corrupt("table %q has no column list", info.name)
	}
	depth, end := 0, -1
	var items [][]token
	var current []token
	for i := open; i < len(tokens) && end < 0; i++ {
		t := tokens[i]
		switch {
		case t.kind == 'p' && t.text == "(":
			depth++
			if depth > 1 {
				current = append(current, t)
			}
		case t.kind == 'p' && t.text == ")":
			depth--
			if depth == 0 {
				items = append(items, current)
				end = i
			} else {
				current = append(current, t)
			}
		case t.kind == 'p' && t.text == "," && depth == 1:
			items = append(items, current)
			current = nil
		default:
			current = append(current, t)
		}
	}
	if end < 0 {
		return corrupt("table %q column list is not closed", info.name)
	}
	if rest := tokens[end+1:]; len(rest) >= 2 && isWord(rest[len(rest)-2], "WITHOUT") && isWord(rest[len(rest)-1], "ROWID") {
		return fmt.Errorf("%w: WITHOUT ROWID table %q", ErrUnsupported, info.name)
	}
	var primary string
	for _, item := range items {
		if len(item) == 0 {
			return corrupt("table %q has an empty column definition", info.name)
		}
		first := item[0]
		if first.kind == 'w' {
			switch strings.ToUpper(first.text) {
			case "CONSTRAINT", "PRIMARY", "UNIQUE", "CHECK", "FOREIGN":
				if column := tablePrimaryKey(item); column != "" {
					primary = column
				}
				continue
			}
		}
		if first.kind != 'w' && first.kind != 'q' {
			return corrupt("table %q has an invalid column definition", info.name)
		}
		if len(info.columns) >= maxColumns {
			return corrupt("table %q has too many columns", info.name)
		}
		column, declared, def, alias, err := parseColumn(item)
		if err != nil {
			return fmt.Errorf("table %q: %w", info.name, err)
		}
		info.columns = append(info.columns, column)
		info.types = append(info.types, declared)
		info.defaults = append(info.defaults, def)
		if alias && info.aliasIdx < 0 {
			info.aliasIdx = len(info.columns) - 1
		}
	}
	if len(info.columns) == 0 {
		return corrupt("table %q has no columns", info.name)
	}
	if primary != "" && info.aliasIdx < 0 {
		for i, column := range info.columns {
			if strings.EqualFold(column, primary) && info.integerType(i) {
				info.aliasIdx = i
			}
		}
	}
	return nil
}

func (info *tableInfo) integerType(index int) bool {
	return index < len(info.types) && info.types[index] == "INTEGER"
}

// tablePrimaryKey returns the column of a single-column table-level PRIMARY KEY.
func tablePrimaryKey(item []token) string {
	for i := 0; i+1 < len(item); i++ {
		if isWord(item[i], "PRIMARY") && isWord(item[i+1], "KEY") {
			var names []string
			for j := i + 2; j < len(item); j++ {
				if item[j].kind == 'w' || item[j].kind == 'q' {
					if isWord(item[j], "ASC") || isWord(item[j], "DESC") || isWord(item[j], "AUTOINCREMENT") || isWord(item[j], "ON") {
						break
					}
					names = append(names, item[j].text)
				}
				if item[j].kind == 'p' && item[j].text == ")" {
					break
				}
			}
			if len(names) == 1 {
				return names[0]
			}
		}
	}
	return ""
}

var constraintWords = map[string]bool{
	"CONSTRAINT": true, "PRIMARY": true, "NOT": true, "NULL": true, "UNIQUE": true, "CHECK": true,
	"DEFAULT": true, "COLLATE": true, "REFERENCES": true, "GENERATED": true, "AS": true,
}

func parseColumn(item []token) (name, declared string, def defaultValue, alias bool, err error) {
	name = item[0].text
	i := 1
	var typeWords []string
	for i < len(item) && item[i].kind == 'w' && !constraintWords[strings.ToUpper(item[i].text)] {
		typeWords = append(typeWords, strings.ToUpper(item[i].text))
		i++
	}
	if i < len(item) && item[i].kind == 'p' && item[i].text == "(" { // type arguments
		for i < len(item) && !(item[i].kind == 'p' && item[i].text == ")") {
			i++
		}
		i++
	}
	declared = strings.Join(typeWords, " ")
	def = defaultValue{known: true} // no DEFAULT means NULL
	for ; i < len(item); i++ {
		t := item[i]
		switch {
		case isWord(t, "GENERATED") || isWord(t, "AS"):
			return "", "", defaultValue{}, false, fmt.Errorf("%w: generated column", ErrUnsupported)
		case isWord(t, "PRIMARY") && i+1 < len(item) && isWord(item[i+1], "KEY"):
			if declared == "INTEGER" {
				alias = true
				// INTEGER PRIMARY KEY DESC is an ordinary column in SQLite.
				for j := i + 2; j < len(item) && j < i+4; j++ {
					if isWord(item[j], "DESC") {
						alias = false
					}
				}
			}
		case isWord(t, "DEFAULT"):
			def = parseDefault(item[i+1:])
		}
	}
	return name, declared, def, alias, nil
}

func parseDefault(rest []token) defaultValue {
	if len(rest) == 0 {
		return defaultValue{}
	}
	sign := int64(1)
	if rest[0].kind == 'p' && (rest[0].text == "-" || rest[0].text == "+") {
		if rest[0].text == "-" {
			sign = -1
		}
		rest = rest[1:]
		if len(rest) == 0 {
			return defaultValue{}
		}
	}
	switch t := rest[0]; t.kind {
	case 'n':
		if i, err := strconv.ParseInt(t.text, 0, 64); err == nil {
			return defaultValue{true, sign * i}
		}
		if f, err := strconv.ParseFloat(t.text, 64); err == nil {
			return defaultValue{true, float64(sign) * f}
		}
	case 's':
		if sign == 1 {
			return defaultValue{true, t.text}
		}
	case 'w':
		switch strings.ToUpper(t.text) {
		case "NULL":
			return defaultValue{true, nil}
		case "TRUE":
			return defaultValue{true, int64(1)}
		case "FALSE":
			return defaultValue{true, int64(0)}
		}
	}
	return defaultValue{} // an expression: unsupported, reported only if a row needs it
}
