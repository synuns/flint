// tools.go — flint mcp 도구 5종(v0.32.0). Dispatch는 cli.Run 인자를 조립해 호출하고
// CLI 출력 문자열과 exit 코드(0/1/2/3 계약)를 그대로 반환한다(실DB·계약 재사용).
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/query"
)

// ToolDef — MCP tools/list 항목. InputSchema는 JSON Schema 객체.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// runFn — seam. 기본 실 cli.Run, 테스트는 fake 주입(SUPERLITE_REAL=1은 실구현).
var runFn = cli.Run

// ToolsManifest — 도구 5종 정의(read·write·hot·pending·approve — v0.32.0).
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
			// v1.0.1 감사 F7: session_id 파라미터 폐지 — CLI hot의
			// --session은 조회에 영향 없는 no-op이었으므로 광고-폐기
			// 불일치. 실 세션 필터링은 신규 기능으로 제외.
			Name:        "flint_hot",
			Description: "주입용 핫 요약(2,000자 상한). 최근 항목 전체(세션 필터 없음).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			// v0.32.0: 검수 회로 — 세션 안에서 대기 지식 심사·승격.
			Name:        "flint_pending",
			Description: "대기 지식 목록 + 규칙 기반 추천. verdict는 promote|merge|expire|hold. JSON 반환.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "flint_approve",
			Description: "대기 지식 승격. supersede 지정 시 구 활성 지식을 새 것으로 대체 병합.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":        map[string]any{"type": "integer", "description": "승격할 pending id"},
					"supersede": map[string]any{"type": "integer", "description": "대체할 구 active id(선택)"},
				},
				"required": []string{"id"},
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
	case "flint_pending":
		return runCall([]string{"pending", "--json"}, strings.NewReader(""))
	case "flint_approve":
		return dispatchApprove(args)
	default:
		// v0.28.0(H③): 이름 이스케이프(원시 결합 JSON 파손 방지).
		if b, err := json.Marshal(map[string]string{"error": "unknown tool: " + name}); err == nil {
			return string(b), 1
		}
		return `{"error":"unknown tool"}`, 1
	}
}

// dispatchRead — flint read [query] --tag --limit 조립. limit은 토큰 예산
// (ClampLimit 상한 50)을 도구 경로에도 박제한다. query는 외부 입력이라
// 플래그로 파싱되지 않게 "--" 뒤에 둔다(N9 — runRead의 end-of-options 규칙).
func dispatchRead(args map[string]any) (string, int) {
	// v1.2.0 T2 잔여: MCP 경로 라벨 — read_events.source='mcp' 계량.
	cliArgs := []string{"--source", "mcp"}
	if tag := strArg(args, "tag"); tag != "" {
		cliArgs = append(cliArgs, "--tag", tag)
	}
	if n, ok := intArg(args, "limit"); ok {
		cliArgs = append(cliArgs, "--limit", strconv.Itoa(query.ClampLimit(n)))
	}
	if q := strArg(args, "query"); q != "" {
		cliArgs = append(cliArgs, "--", q)
	}
	return runCall(append([]string{"read"}, cliArgs...), strings.NewReader(""))
}

// dispatchApprove — flint approve --id N [--supersede OLD]. v0.32.0 검수
// 회로: 신뢰 게이트(write와 동일 신뢰 수준 — 로컬 stdio 단일 사용자).
// id는 양의 정수, supersede는 id와 상이해야 한다 — exit 3(검증 실패 계약).
func dispatchApprove(args map[string]any) (string, int) {
	id, ok := intArgStrict(args, "id")
	if !ok || id < 1 {
		return `{"error":"schema","field":"id","expect":"양의 정수"}`, 3
	}
	cliArgs := []string{"approve", "--id", strconv.Itoa(id)}
	// v0.32.0 리뷰 1: supersede 키가 존재하면 타입·값 오류가 무음 스킵이
	// 아니라 반드시 exit 3 — 문자열 "3" 등 LLM 호출기 오타이 승격만
	// 실행되는 의미 변질을 차단한다.
	if _, has := args["supersede"]; has {
		sup, ok := intArgStrict(args, "supersede")
		if !ok {
			return `{"error":"schema","field":"supersede","expect":"정수(소수 절삭 없음)"}`, 3
		}
		if sup < 1 {
			return `{"error":"schema","field":"supersede","expect":"양의 정수"}`, 3
		}
		if sup == id {
			return `{"error":"schema","field":"supersede","expect":"id와 상이"}`, 3
		}
		cliArgs = append(cliArgs, "--supersede", strconv.Itoa(sup))
	}
	return runCall(cliArgs, strings.NewReader(""))
}

// intArgStrict — 정수성 검사 intArg(v0.32.1 감사 3): 소수(7.9)는 절삭이
// 아니라 실패 — 승격·대체 대상 오지정 방지(read limit의 관대 절삭과
// 구분되는 검증 엄격 계약).
func intArgStrict(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// writeKindExpect — flint write가 수용하는 kind 집합. 원천은
// lib/store/validate.go의 kindExpect(비수출 상수 — MCP 계층에서 재사용
// 불가라 값 계약으로 복제). 변경 시 양쪽을 같이 고친다.
// v1.2.3 R17-B5: 무효 kind의 runFn 도달 전 서버측 차단.
const writeKindExpect = "knowledge|session|project"

// dispatchWrite — flint write <kind> + stdin payload JSON. dedup는 CLI 파이프라인
// 위임으로 도구 호출로도 멱등이다. kind는 화이트리스트 서버측 검증(B5).
func dispatchWrite(args map[string]any) (string, int) {
	kind := strArg(args, "kind")
	if kind == "" {
		return `{"error":"schema","field":"kind","expect":"required"}`, 3
	}
	switch kind {
	case "knowledge", "session", "project":
	default:
		return `{"error":"schema","field":"kind","expect":"` + writeKindExpect + `"}`, 3
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

// dispatchHot — flint hot 조립. v1.2.3 R17-B6: session_id 잔여 제거 —
// v1.0.1 F7로 --session은 조회 무영향 no-op였고 광고에서도 폐기됐다.
// 알 수 없는 인자는 무시한다(도구 계약상 인자 없음).
func dispatchHot(args map[string]any) (string, int) {
	return runCall([]string{"hot"}, strings.NewReader(""))
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

// (v0.20.2 패치 루프 1차 F7: NewSimpleStore·simpleDSN·simpleStore 제거 —
// DispatchTool이 cli.Run으로 각 호출 store를 열면서 미사용 죽은 방어 코드였다.
// 이력은 git을 참조.)
