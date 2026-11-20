// Package backup owns flint's own SQL dump generation and replay per
// ADR-0002. pg_dump does not work on Doltgres (ADR-0002), so this package
// generates the schema DDL and a BEGIN; DELETE; INSERT replay script itself.
package backup

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/synuns/flint/lib/db"
)

var (
	nextvalRe = regexp.MustCompile(`nextval\('([^']+)'`)
	dollarRe  = regexp.MustCompile(`^\$[A-Za-z_]*\$`)
)

// Dump writes the entire public schema (user tables) of the dsn database to
// w as a schema DDL + data replay SQL script.
func Dump(ctx context.Context, dsn string, w io.Writer) error {
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	tables, fks, err := loadTables(ctx, pool)
	if err != nil {
		return err
	}
	order := topoInsert(tables, fks) // parents first (FK targets before referrers)

	fmt.Fprintf(w, "-- flint backup (ADR-0002) generated %s\n", time.Now().UTC().Format(time.RFC3339))

	setvals := map[string]bool{}
	for _, t := range order {
		cols, cons, err := tableDef(ctx, pool, t)
		if err != nil {
			return err
		}
		for _, c := range cols {
			m := nextvalRe.FindStringSubmatch(c.def)
			if m == nil {
				continue
			}
			fmt.Fprintf(w, "CREATE SEQUENCE IF NOT EXISTS %s;\n", m[1])
			setvals[fmt.Sprintf("SELECT setval('%s', COALESCE((SELECT MAX(%s) FROM %s), 1));",
				m[1], quoteIdent(c.name), quoteIdent(t))] = true
		}
		writeCreateTable(w, t, cols, cons)
	}

	fmt.Fprintln(w, "BEGIN;")
	for i := len(order) - 1; i >= 0; i-- { // delete children first (avoids FK conflicts)
		fmt.Fprintf(w, "DELETE FROM %s;\n", quoteIdent(order[i]))
	}
	for _, t := range order {
		if err := dumpRows(ctx, pool, w, t); err != nil {
			return err
		}
	}
	for s := range setvals {
		fmt.Fprintln(w, s)
	}
	fmt.Fprintln(w, "COMMIT;")
	return nil
}

// Restore executes the dump script sequentially in a single transaction.
// Using its own pool (db.Connect) is intentional design — it needs
// transaction+Exec, a surface wire.Store lacks under its write-repo-only
// contract. A one-shot CLI call with defer Close leaks nothing. Do not
// rewire.
func Restore(ctx context.Context, dsn string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, stmt := range splitStatements(string(data)) {
		if stripComments(stmt) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("restore: %v\n  in: %.120s", err, stmt)
		}
	}
	return tx.Commit(ctx)
}

type column struct {
	name, typ, def string
	notNull        bool
}

// loadTables reads the public schema's user table list and FK relations (child -> parents).
func loadTables(ctx context.Context, pool *pgxpool.Pool) ([]string, map[string][]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r'
		  AND c.relname NOT LIKE 'dolt\_%'
		ORDER BY c.relname`)
	if err != nil {
		return nil, nil, err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, nil, err
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	fkRows, err := pool.Query(ctx, `
		SELECT tc.relname, tf.relname FROM pg_constraint con
		JOIN pg_class tc ON tc.oid = con.conrelid
		JOIN pg_class tf ON tf.oid = con.confrelid
		JOIN pg_namespace n ON n.oid = tc.relnamespace
		WHERE con.contype = 'f' AND n.nspname = 'public'`)
	if err != nil {
		return nil, nil, err
	}
	fks := map[string][]string{}
	for fkRows.Next() {
		var child, parent string
		if err := fkRows.Scan(&child, &parent); err != nil {
			return nil, nil, err
		}
		fks[child] = append(fks[child], parent)
	}
	return tables, fks, fkRows.Err()
}

// topoInsert orders tables so FK parents come first (on a cycle, the remainder keeps its original order).
func topoInsert(tables []string, fks map[string][]string) []string {
	inDump := map[string]bool{}
	for _, t := range tables {
		inDump[t] = true
	}
	var ordered []string
	placed := map[string]bool{}
	for len(ordered) < len(tables) {
		progress := false
		for _, t := range tables {
			if placed[t] {
				continue
			}
			ready := true
			for _, p := range fks[t] {
				if inDump[p] && !placed[p] {
					ready = false
					break
				}
			}
			if ready {
				ordered = append(ordered, t)
				placed[t] = true
				progress = true
			}
		}
		if !progress {
			for _, t := range tables {
				if !placed[t] {
					ordered = append(ordered, t)
					placed[t] = true
				}
			}
		}
	}
	return ordered
}

// tableDef reads column definitions and PK/UNIQUE/FOREIGN KEY constraints
// from the catalog. Doltgres leaves pg_attrdef empty, so column defaults
// come from information_schema.
func tableDef(ctx context.Context, pool *pgxpool.Pool, table string) ([]column, [][2]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, udt_name, udt_schema,
		       is_nullable = 'NO',
		       character_maximum_length, numeric_precision, numeric_scale,
		       column_default
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, nil, err
	}
	var cols []column
	for rows.Next() {
		var c column
		var dataType, udtName, udtSchema string
		var charLen *int
		var numPrec, numScale *int
		var def *string
		if err := rows.Scan(&c.name, &dataType, &udtName, &udtSchema, &c.notNull,
			&charLen, &numPrec, &numScale, &def); err != nil {
			return nil, nil, err
		}
		c.typ = columnType(dataType, udtName, udtSchema, charLen, numPrec, numScale)
		if def != nil {
			c.def = *def
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	consRows, err := pool.Query(ctx, `
		SELECT conname, pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid = $1::regclass AND contype IN ('p','u','f')
		ORDER BY contype, conname`, "public."+table)
	if err != nil {
		return nil, nil, err
	}
	var cons [][2]string
	for consRows.Next() {
		var name, def string
		if err := consRows.Scan(&name, &def); err != nil {
			return nil, nil, err
		}
		cons = append(cons, [2]string{name, def})
	}
	return cols, cons, consRows.Err()
}

// columnType assembles the column type string from information_schema data.
// Only types with length or precision get parenthesized notation.
func columnType(dataType, udtName, udtSchema string, charLen, numPrec, numScale *int) string {
	if dataType == "ARRAY" {
		return udtName // udt_name arrives as elem_type[]
	}
	if udtSchema != "pg_catalog" {
		return udtSchema + "." + udtName
	}
	switch dataType {
	case "character varying", "character", "bit varying":
		if charLen != nil {
			return fmt.Sprintf("%s(%d)", dataType, *charLen)
		}
	case "numeric":
		if numPrec != nil {
			if numScale != nil {
				return fmt.Sprintf("numeric(%d,%d)", *numPrec, *numScale)
			}
			return fmt.Sprintf("numeric(%d)", *numPrec)
		}
	}
	return dataType
}

func writeCreateTable(w io.Writer, table string, cols []column, cons [][2]string) {
	var parts []string
	for _, c := range cols {
		p := quoteIdent(c.name) + " " + c.typ
		if c.notNull {
			p += " NOT NULL"
		}
		if c.def != "" {
			p += " DEFAULT " + c.def
		}
		parts = append(parts, p)
	}
	for _, c := range cons {
		parts = append(parts, fmt.Sprintf("CONSTRAINT %s %s", quoteIdent(c[0]), c[1]))
	}
	fmt.Fprintf(w, "CREATE TABLE IF NOT EXISTS %s (\n  %s\n);\n",
		quoteIdent(table), strings.Join(parts, ",\n  "))
}

// dumpRows writes every row of the table as INSERT statements.
func dumpRows(ctx context.Context, pool *pgxpool.Pool, w io.Writer, table string) error {
	rows, err := pool.Query(ctx, `SELECT * FROM `+quoteIdent(table))
	if err != nil {
		return err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	var names []string
	for _, f := range fields {
		names = append(names, quoteIdent(string(f.Name)))
	}
	colspec := strings.Join(names, ", ")
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return err
		}
		lits := make([]string, len(vals))
		for i, v := range vals {
			lits[i] = sqlLit(v)
		}
		fmt.Fprintf(w, "INSERT INTO %s (%s) VALUES (%s);\n",
			quoteIdent(table), colspec, strings.Join(lits, ", "))
	}
	return rows.Err()
}

// sqlLit converts a Go value to an SQL literal (doubling single quotes).
func sqlLit(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	case time.Time:
		return "'" + x.Format("2006-01-02 15:04:05.999999999-07:00") + "'"
	case []byte:
		return "'\\x" + hex.EncodeToString(x) + "'"
	default:
		return fmt.Sprintf("%v", x)
	}
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// splitStatements splits an SQL script into statements. Semicolons inside
// single-quoted strings (doubled quotes), -- line comments, block comments,
// and dollar quoting ($tag$...$tag$) are not treated as separators.
func splitStatements(script string) []string {
	var stmts []string
	var cur strings.Builder
	i := 0
	for i < len(script) {
		c := script[i]
		switch {
		case c == '\'':
			j := i + 1
			for j < len(script) {
				if script[j] == '\'' {
					if j+1 < len(script) && script[j+1] == '\'' {
						j += 2 // '' escape
						continue
					}
					break
				}
				j++
			}
			end := min(j+1, len(script))
			cur.WriteString(script[i:end])
			i = end
		case c == '-' && i+1 < len(script) && script[i+1] == '-':
			j := strings.IndexByte(script[i:], '\n')
			end := len(script)
			if j >= 0 {
				end = i + j
			}
			cur.WriteString(script[i:end])
			i = end
		case c == '/' && i+1 < len(script) && script[i+1] == '*':
			end := len(script)
			if j := strings.Index(script[i+2:], "*/"); j >= 0 {
				end = i + 2 + j + 2
			}
			cur.WriteString(script[i:end])
			i = end
		case c == '$' && dollarRe.MatchString(script[i:]):
			tag := dollarRe.FindString(script[i:])
			end := len(script)
			if j := strings.Index(script[i+len(tag):], tag); j >= 0 {
				end = i + len(tag) + j + len(tag)
			}
			cur.WriteString(script[i:end])
			i = end
		case c == ';':
			if s := strings.TrimSpace(cur.String()); s != "" {
				stmts = append(stmts, s)
			}
			cur.Reset()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

// stripComments strips leading comments (to skip comment-only statements).
func stripComments(stmt string) string {
	s := stmt
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s[2:], "*/"); i >= 0 {
				s = s[2+i+2:]
				continue
			}
			return ""
		default:
			return s
		}
	}
}
