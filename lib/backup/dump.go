// Package backup 은 ADR-0002에 따른 flint 자체 SQL 덤프 생성·재생을 담당한다.
// pg_dump는 Doltgres에서 동작하지 않으므로(ADR-0002), 스키마 DDL과
// BEGIN; DELETE; INSERT 재생 스크립트를 직접 생성한다.
package backup

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"flint/lib/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	nextvalRe = regexp.MustCompile(`nextval\('([^']+)'`)
	dollarRe  = regexp.MustCompile(`^\$[A-Za-z_]*\$`)
)

// Dump 는 dsn 데이터베이스의 public 스키마(사용자 테이블) 전체를
// 스키마 DDL + 데이터 재생 SQL 스크립트로 w에 기록한다.
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
	order := topoInsert(tables, fks) // 부모 우선(FK 참조 대상 먼저)

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
	for i := len(order) - 1; i >= 0; i-- { // 자식 먼저 삭제(FK 충돌 회피)
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

// Restore 는 덤프 스크립트를 단일 트랜잭션으로 순차 실행한다.
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

// loadTables 는 public 스키마의 사용자 테이블 목록과 FK 관계(child→parents)를 읽는다.
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

// topoInsert 는 FK 부모가 먼저 오도록 정렬한다(사이클이면 잔여를 원순으로).
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

// tableDef 는 컬럼 정의와 PK/UNIQUE/FOREIGN KEY 제약을 카탈로그에서 읽는다.
// Doltgres는 pg_attrdef을 비워두므로 컬럼 기본값은 information_schema에서 얻는다.
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

// columnType 는 information_schema 정보로 컬럼 타입 문자열을 조립한다.
// 길이·정밀도가 있는 타입만 괄호 표기를 붙인다.
func columnType(dataType, udtName, udtSchema string, charLen, numPrec, numScale *int) string {
	if dataType == "ARRAY" {
		return udtName // udt_name이 elem_type[] 형태로 온다
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

// dumpRows 는 테이블 전체 행을 INSERT 문으로 기록한다.
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

// sqlLit 는 Go 값을 SQL 리터럴로 변환한다(작은따옴표 '' 이스케이프).
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

// splitStatements 는 SQL 스크립트를 문장 단위로 쪼갠다.
// 작은따옴표 문자열('' 이스케이프), -- 한 줄 주석, 블록 주석,
// 달러 인용($tag$...$tag$) 안의 세미콜론은 분리자로 취급하지 않는다.
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
						j += 2 // '' 이스케이프
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

// stripComments 는 선행 주석을 걷어낸다(주석뿐인 문장 건너뛰기용).
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
