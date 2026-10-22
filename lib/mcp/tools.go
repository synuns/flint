// tools.go — flint mcp 도구 3종. Dispatch는 cli.Run 인자를 조립해 호출하고
// CLI 출력 문자열과 exit 코드(0/1/2/3 계약)를 그대로 반환한다(실DB·계약 재사용).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/cli"
	"flint/lib/db"
	"flint/lib/query"
)

// ToolDef — MCP tools/list 항목. InputSchema는 JSON Schema 객체.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// runFn — seam. 기본 실 cli.Run, 테스트는 fake 주입(SUPERLITE_REAL=1은 실구현).
var runFn = cli.Run

// ToolsManifest — 도구 3종 정의(flint read/write/hot 대응).
func ToolsManifest() []ToolDef {
	return []ToolDef{
		{
			Name:        "flint_read",
			Description: "활성 knowledge 검색(풀 방식). query/tag로 필터, limit 상한 50.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"tag":   map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer", "maximum": 50},
				},
			},
		},
		{
			Name:        "flint_write",
			Description: "결정·지식 기록(kind+payload, 멱등: 동일 payload는 duplicate).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"kind", "payload"},
				"properties": map[string]any{
					"kind":    map[string]any{"type": "string", "enum": []string{"knowledge", "session", "project"}},
					"payload": map[string]any{"type": "object"},
				},
			},
		},
		{
			Name:        "flint_hot",
			Description: "주입용 핫 요약(2,000자 상한). session_id 생략 시 최근 전체.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
				},
			},
		},
	}
}

// DispatchTool — 도구 이름과 인자를 cli.Run 호출로 조립해 실행한다.
// 반환 = (CLI 출력 한 줄, exit 코드). 미정의 도구는 exit 1.
func DispatchTool(name string, args map[string]any) (string, int) {
	switch name {
	case "flint_read":
		return dispatchRead(args)
	case "flint_write":
		return dispatchWrite(args)
	case "flint_hot":
		return dispatchHot(args)
	default:
		return fmt.Sprintf(`{"error":"unknown tool: %s"}`, name), 1
	}
}

// dispatchRead — flint read [query] --tag --limit 조립. limit은 토큰 예산
// (ClampLimit 상한 50)을 도구 경로에도 박제한다.
func dispatchRead(args map[string]any) (string, int) {
	var cliArgs []string
	if q := strArg(args, "query"); q != "" {
		cliArgs = append(cliArgs, q)
	}
	if tag := strArg(args, "tag"); tag != "" {
		cliArgs = append(cliArgs, "--tag", tag)
	}
	if n, ok := intArg(args, "limit"); ok {
		cliArgs = append(cliArgs, "--limit", strconv.Itoa(query.ClampLimit(n)))
	}
	return runCall(append([]string{"read"}, cliArgs...), strings.NewReader(""))
}

// dispatchWrite — flint write <kind> + stdin payload JSON. dedup는 CLI 파이프라인
// 위임으로 도구 호출로도 멱등이다.
func dispatchWrite(args map[string]any) (string, int) {
	kind := strArg(args, "kind")
	if kind == "" {
		return `{"error":"schema","field":"kind","expect":"required"}`, 3
	}
	payload, ok := args["payload"]
	if !ok {
		return `{"error":"schema","field":"payload","expect":"required"}`, 3
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"error":"schema","field":"payload","expect":"valid JSON: %s"}`, err), 3
	}
	return runCall([]string{"write", kind, "--source", "mcp"}, bytes.NewReader(b))
}

// dispatchHot — flint hot --session 조립.
func dispatchHot(args map[string]any) (string, int) {
	var cliArgs []string
	if s := strArg(args, "session_id"); s != "" {
		cliArgs = append(cliArgs, "--session", s)
	}
	return runCall(append([]string{"hot"}, cliArgs...), strings.NewReader(""))
}

// runCall — 조립된 인자로 runFn 실행, 출력의 끝 개행만 제거해 반환.
func runCall(cliArgs []string, stdin io.Reader) (string, int) {
	var out bytes.Buffer
	code := runFn(cliArgs, stdin, &out)
	return strings.TrimRight(out.String(), "\n"), code
}

// strArg — map에서 문자열 인자 추출(JSON은 문자열만).
func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// intArg — map에서 정수 인자 추출. JSON 디코딩 결과는 float64.
func intArg(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// Store — mcp 읽기 경로(flint_read/flint_hot) 전용 최소 인터페이스.
// simple protocol 커넥션이면 뷰 재정의가 즉시 가시다(장수 세션 describe 캐시 우회).
type Store interface {
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
	Close()
}

// connectSimple — seam. 기본 실구현(db.Connect 풀), 테스트는 fake 주입.
var connectSimple = func(ctx context.Context, dsn string) (Store, error) {
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &simpleStore{pool: pool}, nil
}

// NewSimpleStore — dsn에 default_query_exec_mode=simple_protocol를 붙인
// 읽기 전용 저장소를 만든다(MCP 뷰 캐시 방어, ADR-0006 인접). simple 모드는
// 준비된 문장 describe 캐시를 쓰지 않아 placeholder($1) 파라미터도 정상 동작한다.
// write 경로는 기존 cli.Run 위임 그대로(불변).
func NewSimpleStore(dsn string) (Store, error) {
	return connectSimple(context.Background(), simpleDSN(dsn))
}

// simpleDSN — DSN에 simple protocol 실행 모드 파라미터 추가(구분자 자동).
func simpleDSN(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "default_query_exec_mode=simple_protocol"
}

// simpleStore — pgxpool 어댑터. Query는 행 값 전체를 반환한다(cli.realStore와 동일 계약).
type simpleStore struct {
	pool *pgxpool.Pool
}

func (s *simpleStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func (s *simpleStore) Close() { s.pool.Close() }
