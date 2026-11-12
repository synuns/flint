// hot_single_view_test.go — v0.41.0 T5: hot_view 단일 정의 기반 증분.
// 동반 질의(hotKnowledgeIDsSQL·pendingValidAtsSQL) 폐지 — id·valid_at를
// hot_view 본질의에서 직접 소비해 주입 집합=증분 집합을 구조적으로
// 보장한다(Review Focus: 동반 질의 제거 후에도 주입=증분·스냅숏 정합).
package knowledge

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/sqlfs"
)

// hotBumpRecorder — BumpReads·RecordHotEvent 계측을 녹음하는 fake
// (fakeStore.Query가 질의 로그를 기록한다).
type hotBumpRecorder struct {
	*fakeStore
	bumped []int
	events []string
}

func (h *hotBumpRecorder) BumpReads(ctx context.Context, ids []int) error {
	h.bumped = append(h.bumped, ids...)
	return nil
}

func (h *hotBumpRecorder) RecordHotEvent(ctx context.Context, ids string) error {
	h.events = append(h.events, ids)
	return nil
}

// TestHotViewSingleDefinition — Review Focus(views.sql 3팔 단일 정의):
// hot_view 세 팔이 id·valid_at·ingested_at을 말미에 노출한다(프로젝트
// 팔은 NULL). 세 팔 조건을 다시 모은 평행 정의(동반 질의)는 폐지.
func TestHotViewSingleDefinition(t *testing.T) {
	v := sqlfs.ViewsSQL()
	i := strings.Index(v, "CREATE OR REPLACE VIEW hot_view AS")
	if i < 0 {
		t.Fatal("views.sql에 hot_view 정의 없음")
	}
	arms := strings.Split(v[i:], "UNION ALL")
	if len(arms) != 3 {
		t.Fatalf("hot_view 팔 수 = %d, want 3(단일 정의): %s", len(arms), v[i:])
	}
	// 프로젝트 팔: id·valid_at·ingested_at 모두 NULL.
	for _, frag := range []string{
		"NULL::int AS id", "NULL::timestamptz AS valid_at", "NULL::timestamptz AS ingested_at",
	} {
		if !strings.Contains(arms[0], frag) {
			t.Errorf("hot_view 프로젝트 팔에 %q 누락: %s", frag, arms[0])
		}
	}
	// 지식 팔(pending·decision): k.id·k.valid_at·k.ingested_at 노출.
	for ai, arm := range arms[1:] {
		for _, frag := range []string{"k.id", "k.valid_at", "k.ingested_at"} {
			if !strings.Contains(arm, frag) {
				t.Errorf("hot_view 지식 팔 %d에 %q 누락: %s", ai+1, frag, arm)
			}
		}
	}
	// HotSQL은 이 뷰에서 증분·접미 컬럼까지 1개 질의로 조회한다.
	sql := query.HotSQL("")
	for _, frag := range []string{
		"FROM hot_view",
		"id, valid_at, ingested_at", // 단일 질의가 말미 3컬럼까지 선택
		"ORDER BY ts DESC, id DESC", // tie-break 유지(CI 39713526850 룰링)
		"LIMIT " + strconv.Itoa(query.ClampLimit(0)),
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("HotSQL에 %q 누락: %s", frag, sql)
		}
	}
}

// TestHotFullSingleQueryInjectionEqualsIncrement — Review Focus(주입=증분):
// --full 경로는 hot_view 본질의 1개 질의만으로 주입·증분·접미를 전부
// 소비한다. 동반 질의("FROM projects p" 재조합·"FROM knowledge" valid_at
// 별도 조회)가 나가지 않고, 주입된(생존) 지식 id 전수가 증분·노출 기록과
// 정합한다.
func TestHotFullSingleQueryInjectionEqualsIncrement(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour) // 내림 2일 → ·+2일
	ts := time.Now().UTC()
	rows := [][]any{
		{"project", "p1", "active", ts, nil, nil, nil},
		{"pending", "토픽", "요약", ts.Add(-time.Minute), int32(3), validAt, validAt},
		{"decision", "토픽2", "요약2", ts.Add(-2 * time.Minute), int32(7), ts, ts},
	}
	f := &hotBumpRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return rows, nil
		}
		return nil, nil // meta(hot_budget·hot_stats) 등: 미설정
	}}}
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	// 증분 = 생존 지식 id 전수(프로젝트 팔 제외) — 본질의 id 컬럼에서.
	if len(f.bumped) != 2 || f.bumped[0] != 3 || f.bumped[1] != 7 {
		t.Errorf("BumpReads ids = %v, want [3 7](본질의 id 컬럼 — 동반 질의 없이)", f.bumped)
	}
	if len(f.events) != 1 || f.events[0] != "3,7" {
		t.Errorf("RecordHotEvent = %v, want [3,7]", f.events)
	}
	// pending 접미는 본질의 valid_at 컬럼에서(동반 질의 폐지).
	if !strings.Contains(out.String(), "·+2일") {
		t.Errorf("pending 대기일수 접미 누락: %q", out.String())
	}
	// 동반 질의 폐지: hot_view 질의는 정확히 1회, 지식 재조합 질의 0회.
	hotQ, projQ, knowQ := 0, 0, 0
	for _, q := range f.fakeStore.queries {
		switch {
		case strings.Contains(q, "FROM hot_view"):
			hotQ++
		case strings.Contains(q, "FROM projects p"):
			projQ++
		case strings.Contains(q, "FROM knowledge"):
			knowQ++
		}
	}
	if hotQ != 1 || projQ != 0 || knowQ != 0 {
		t.Errorf("질의 배치 = hot_view %d·projects 재조합 %d·knowledge 동반 %d, want 1·0·0",
			hotQ, projQ, knowQ)
	}
}

// TestHotFullSuffixOmittedWithoutValidAtCell — 본질의 valid_at 셀 부재
// (구형 뷰 모양 4열 행)·time.Time 아닌 값은 접미 생략 — 기존 출력·
// exit 불변 승계(동반 질의 0행·오류 승계 계약의 단일 정의 판본).
func TestHotFullSuffixOmittedWithoutValidAtCell(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	cases := []struct {
		name string
		rows [][]any
	}{
		{"셀 부재(구형 뷰)", [][]any{{"pending", "토픽", "요약", "2026-10-22"}}},
		{"time.Time 아닌 값", [][]any{{"pending", "토픽", "요약", "2026-10-22", int32(3), "not-time", nil}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
				if strings.Contains(sql, "FROM hot_view") {
					return tc.rows, nil
				}
				return nil, nil
			}}
			var out bytes.Buffer
			if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
			}
			if want := query.FormatRows(tc.rows, HotFields, false); out.String() != want {
				t.Fatalf("출력 = %q, want %q(기본 포맷 승계)", out.String(), want)
			}
		})
	}
}
