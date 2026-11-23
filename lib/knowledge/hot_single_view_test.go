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

	for _, frag := range []string{
		"NULL::int AS id", "NULL::timestamptz AS valid_at", "NULL::timestamptz AS ingested_at",
	} {
		if !strings.Contains(arms[0], frag) {
			t.Errorf("hot_view 프로젝트 팔에 %q 누락: %s", frag, arms[0])
		}
	}

	for ai, arm := range arms[1:] {
		for _, frag := range []string{"k.id", "k.valid_at", "k.ingested_at"} {
			if !strings.Contains(arm, frag) {
				t.Errorf("hot_view 지식 팔 %d에 %q 누락: %s", ai+1, frag, arm)
			}
		}
	}

	sql := query.HotSQL("")
	for _, frag := range []string{
		"FROM hot_view",
		"id, valid_at, ingested_at",
		"ORDER BY ts DESC, id DESC",
		"LIMIT " + strconv.Itoa(query.ClampLimit(0)),
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("HotSQL에 %q 누락: %s", frag, sql)
		}
	}
}

func TestHotFullSingleQueryInjectionEqualsIncrement(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour)
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
		return nil, nil
	}}}
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}

	if len(f.bumped) != 2 || f.bumped[0] != 3 || f.bumped[1] != 7 {
		t.Errorf("BumpReads ids = %v, want [3 7](본질의 id 컬럼 — 동반 질의 없이)", f.bumped)
	}
	if len(f.events) != 1 || f.events[0] != "3,7" {
		t.Errorf("RecordHotEvent = %v, want [3,7]", f.events)
	}

	if !strings.Contains(out.String(), "·+2d") {
		t.Errorf("pending 대기일수 접미 누락: %q", out.String())
	}

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

func TestHotRecordsBytes(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	rows := [][]any{
		{"pending", "토픽", "요약 본문", time.Now().UTC(), int32(3), time.Now().UTC(), time.Now().UTC()},
		{"decision", "토픽2", strings.Repeat("긴", 40), time.Now().UTC(), int32(7), time.Now().UTC(), time.Now().UTC()},
	}
	t.Run("--full — ClampHot 후 길이", func(t *testing.T) {
		st := &bytesRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "FROM hot_view"):
				return rows, nil
			case strings.Contains(sql, "FROM meta"):
				return [][]any{{"30"}}, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunHot(ctx, st, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
		}
		if len(st.recorded) != 1 || st.recorded[0].kind != "hot" {
			t.Fatalf("RecordBytes 기록 = %v, want 1건(kind=hot)", st.recorded)
		}
		if got := st.recorded[0].n; got != len(out.String()) {
			t.Errorf("hot bytes = %d, want %d(ClampHot '후' 최종 출력 길이)", got, len(out.String()))
		}
		if unclamped := hotFullOutput(hotSuffixedLines(rows, time.Now().UTC())); len(out.String()) >= len(unclamped) {
			t.Errorf("절단 미발생 — '후' 길이 단언이 무의미: %d >= %d", len(out.String()), len(unclamped))
		}
	})
	t.Run("헤드라인 — 1줄 쓴 총량", func(t *testing.T) {
		st := &bytesRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM hot_view") {
				return rows, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunHot(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot exit = %d: %s", code, out.String())
		}
		if len(st.recorded) != 1 || st.recorded[0].kind != "hot" {
			t.Fatalf("RecordBytes 기록 = %v, want 1건(kind=hot)", st.recorded)
		}
		if got := st.recorded[0].n; got != len(out.String()) {
			t.Errorf("hot bytes = %d, want %d(헤드라인 1줄+개행 — 쓴 총량)", got, len(out.String()))
		}
	})
}

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
