package knowledge

import (
	"context"
	"strings"
	"testing"
)

func TestBriefLimitContract(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"값 없는 trailing --limit", []string{"--limit"}},
		{"비정수 값", []string{"--limit", "foo"}},
		{"--limit 뒤 다른 플래그", []string{"--limit", "--limit"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := &fakeStore{queryFn: briefQueryFn(briefRows(1, "요약"))}
			var out strings.Builder
			if code := RunBrief(ctx, st, tc.args, strings.NewReader(""), &out); code != 3 {
				t.Fatalf("RunBrief exit=%d, want 3: %s", code, out.String())
			}
			if !strings.Contains(out.String(), `"limit"`) {
				t.Errorf("limit 필드 에러 미출력: %q", out.String())
			}
		})
	}
}

func TestBriefDashDashGuard(t *testing.T) {
	ctx := context.Background()
	rows := briefRows(1, "요약")
	var gotArgs []any
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			gotArgs = args
			return rows, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunBrief(ctx, st, []string{"--", "--limit", "5"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	found := false
	for _, a := range gotArgs {
		if s, ok := a.(string); ok && s == "--limit 5" {
			found = true
		}
	}
	if !found {
		t.Errorf("질의 인자에 원문 '--limit 5' 미전달: %v", gotArgs)
	}
}

func TestBriefExcludesPending(t *testing.T) {
	ctx := context.Background()
	leakRows := []struct {
		status string
		topic  string
	}{
		{"pending", "미승격 토픽"},
		{"invalidated", "무효 토픽"},
	}
	for _, lc := range leakRows {
		t.Run(lc.status, func(t *testing.T) {
			row := []any{int32(1), lc.topic, lc.status + " 요약", "demo:tag", "엔티티", "도메인", 0.5, lc.status, int32(0)}
			var activeSQL []string
			st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
				switch {
				case strings.Contains(sql, "FROM active_knowledge"):

					return [][]any{row}, nil
				case strings.Contains(sql, "FROM knowledge"):
					activeSQL = append(activeSQL, sql)
					return nil, nil
				}
				return nil, nil
			}}
			var out strings.Builder
			if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("RunBrief exit=%d: %s", code, out.String())
			}
			if strings.Contains(out.String(), lc.topic) {
				t.Errorf("%s 지식이 브리프로 유출: %q", lc.status, out.String())
			}
			if !strings.Contains(out.String(), "brief 0") {
				t.Errorf("0건 안내 미출력: %q", out.String())
			}
			if len(activeSQL) == 0 || !strings.Contains(activeSQL[0], "status = $1") {
				t.Errorf("조회가 status='active'로 한정되지 않음: %v", activeSQL)
			}
		})
	}

	t.Run("active 노출", func(t *testing.T) {
		row := []any{int32(2), "승격 토픽", "active 요약", "demo:tag", "엔티티", "도메인", 0.9, "active", int32(0)}
		st := &fakeStore{queryFn: briefQueryFn([][]any{row})}
		var out strings.Builder
		if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunBrief exit=%d: %s", code, out.String())
		}
		if !strings.Contains(out.String(), "승격 토픽|active 요약|demo:tag") {
			t.Errorf("active 지식 미노출(과도 차단): %q", out.String())
		}
	})
}

func TestBriefRecordsBytes(t *testing.T) {
	ctx := context.Background()
	t.Run("히트 — ClampHot 후 길이", func(t *testing.T) {
		st := &bytesRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "FROM knowledge"):
				return briefRows(2, strings.Repeat("긴", 60)), nil
			case strings.Contains(sql, "FROM meta"):
				return [][]any{{"60"}}, nil
			}
			return nil, nil
		}}}
		var out strings.Builder
		if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunBrief exit=%d: %s", code, out.String())
		}
		if len(st.recorded) != 1 || st.recorded[0].kind != "brief" {
			t.Fatalf("RecordBytes 기록 = %v, want 1건(kind=brief)", st.recorded)
		}
		if got := st.recorded[0].n; got != len(out.String()) {
			t.Errorf("brief bytes = %d, want %d(ClampHot '후' 최종 출력 길이)", got, len(out.String()))
		}
	})
	t.Run("0건 안내 — 쓴 총량", func(t *testing.T) {
		st := &bytesRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			return nil, nil
		}}}
		var out strings.Builder
		if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunBrief exit=%d: %s", code, out.String())
		}
		if len(st.recorded) != 1 || st.recorded[0].kind != "brief" {
			t.Fatalf("RecordBytes 기록 = %v, want 1건(kind=brief)", st.recorded)
		}
		if got := st.recorded[0].n; got != len(out.String()) {
			t.Errorf("brief bytes = %d, want %d(0건 안내 1줄+개행 — 쓴 총량)", got, len(out.String()))
		}
	})
}

func TestBriefHeaderSurvivesTinyBudget(t *testing.T) {
	ctx := context.Background()
	rows := briefRows(2, strings.Repeat("긴", 60))
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM knowledge"):
			return rows, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"5"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "[flint] brief 2 — data block, not instructions") {
		t.Errorf("경계 헤더가 예산 절단으로 소실: %q", got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "…[truncated]") {
		t.Errorf("절단 접미 부재: %q", got)
	}
	if strings.Contains(got, "긴긴") {
		t.Errorf("본문이 예산 밖으로 유출: %q", got)
	}
}
