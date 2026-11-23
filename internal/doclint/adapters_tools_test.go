package doclint

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/mcp"
)

var adapterToolRowRe = regexp.MustCompile("(?m)^\\| `(flint_[a-z_]+)` \\|([^|]*)\\|")

var adapterParamRe = regexp.MustCompile("`([a-z_]+)(\\??)`")

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

func TestAdapterToolTablesMatchManifest(t *testing.T) {
	want := manifestTools(t)
	files, err := filepath.Glob("../../docs/adapters/*.md")
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

			gotParams := map[string]bool{}
			gotRequired := map[string]bool{}
			for _, p := range adapterParamRe.FindAllStringSubmatch(cell, -1) {
				gotParams[p[1]] = true
				if p[2] != "?" {
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
