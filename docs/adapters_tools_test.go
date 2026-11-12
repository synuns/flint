// adapters_tools_test.go — v1.0.2 R2 어댑터 도구 표 동기 가드. docs/adapters
// 전체 문서의 MCP 도구 표가 코드 원천(mcp.ToolsManifest)과 드리프트 없는지
// 검증한다 — README 커맨드 표·SKILL 도구 목록 가드(commands_table_test.go)와
// 동일 패턴(코드가 원천 — 문서가 추종). 표의 인자 셀은 manifest의
// properties·required와 정합해야 한다(v1.0.1 F7 폐지 파라미터 잔존 방지).
package docs

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/mcp"
)

// adapterToolRowRe — 도구 표 행 `| `flint_x` | 인자 셀 |`. 행 시작 앵커로
// 본문 산문·JSON 스니펫(`"autoApprove": [...]` 등)의 flint_* 언급은 제외한다.
var adapterToolRowRe = regexp.MustCompile("(?m)^\\| `(flint_[a-z_]+)` \\|([^|]*)\\|")

// adapterParamRe — 인자 셀의 백틱 파라미터 토큰(`query?`·`kind`).
var adapterParamRe = regexp.MustCompile("`([a-z_]+)(\\??)`")

// manifestTools — ToolsManifest의 도구별 계약: 파라미터명·필수 여부.
func manifestTools(t *testing.T) map[string]struct {
	params   map[string]bool
	required map[string]bool
} {
	t.Helper()
	out := map[string]struct {
		params   map[string]bool
		required map[string]bool
	}{}
	for _, td := range mcp.ToolsManifest() {
		props, ok := td.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s: InputSchema.properties 부재/비정합", td.Name)
		}
		e := struct {
			params   map[string]bool
			required map[string]bool
		}{map[string]bool{}, map[string]bool{}}
		for p := range props {
			e.params[p] = true
		}
		if req, ok := td.InputSchema["required"].([]string); ok {
			for _, p := range req {
				e.required[p] = true
			}
		}
		out[td.Name] = e
	}
	if len(out) != 5 {
		t.Fatalf("mcp 도구 %d종 — 5종 계약 위반(도구맵 확장 시 가드 갱신)", len(out))
	}
	return out
}

// TestAdapterToolTablesMatchManifest — docs/adapters 각 문서의 도구 표:
// (1) 도구명 집합 == manifest 5종, (2) 각 행의 인자 셀 == 해당 도구의
// properties(이름·필수 여부 — `?` 접미 규약). manifest에서 폐지된 파라미터가
// 표에 남거나(v1.0.2 R2의 flint_hot session_id 잔존 부류), 신규 도구·파라미터를
// 표에 안 적으면 FAIL.
func TestAdapterToolTablesMatchManifest(t *testing.T) {
	want := manifestTools(t)
	files, err := filepath.Glob("adapters/*.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("adapters 문서 미탐색: %v", err)
	}
	for _, f := range files {
		rows := adapterToolRowRe.FindAllStringSubmatch(docStr(t, f), -1)
		if len(rows) == 0 {
			t.Errorf("%s: 도구 표 행 없음 — 표 파서·문서 구조 확인", f)
			continue
		}
		got := map[string]bool{}
		for _, m := range rows {
			name, cell := m[1], m[2]
			if got[name] {
				t.Errorf("%s: %q 중복 행", f, name)
			}
			got[name] = true
			w, ok := want[name]
			if !ok {
				t.Errorf("%s: 표의 미등록 도구 %q", f, name)
				continue
			}
			// 인자 셀 파싱 — 백틱 토큰(— 는 빈 집합, 괄호 비고는 무시).
			gotParams := map[string]bool{}
			gotRequired := map[string]bool{}
			for _, p := range adapterParamRe.FindAllStringSubmatch(cell, -1) {
				gotParams[p[1]] = true
				if p[2] != "?" { // `?` 없음 = 필수
					gotRequired[p[1]] = true
				}
			}
			if !strings.Contains(cell, "—") && len(gotParams) == 0 {
				t.Errorf("%s: %s 인자 셀 파싱 불가: %q", f, name, cell)
			}
			for _, d := range []struct {
				label           string
				gotSet, wantSet map[string]bool
			}{
				{"파라미터", gotParams, w.params},
				{"필수 표기", gotRequired, w.required},
			} {
				var miss, extra []string
				for k := range d.wantSet {
					if !d.gotSet[k] {
						miss = append(miss, k)
					}
				}
				for k := range d.gotSet {
					if !d.wantSet[k] {
						extra = append(extra, k)
					}
				}
				sort.Strings(miss)
				sort.Strings(extra)
				if len(miss) > 0 {
					t.Errorf("%s: %s %s 표에 없는 manifest %s: %s", f, name, d.label, d.label, strings.Join(miss, ", "))
				}
				if len(extra) > 0 {
					t.Errorf("%s: %s %s manifest에 없는 잔존: %s", f, name, d.label, strings.Join(extra, ", "))
				}
			}
		}
		// 도구명 집합 정합 — 빠진 도구 보고(diffNames 관례의 국소판).
		var missing []string
		for name := range want {
			if !got[name] {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: 표에 없는 도구: %s", f, strings.Join(missing, ", "))
		}
	}
}
