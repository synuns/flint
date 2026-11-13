// brief_v0341_test.go — v0.34.1 사후 감사 회귀: read와의 계약 정합
// (N9 `--` 가드·N10 값 없는 trailing 플래그 exit 3)과 예산 절단이
// [flint] 경계 헤더를 잘라내지 않는 방어(ADR-0006).
// v0.39.0 T4: lib/cli에서 이동(cli/brief_v0341_test.go 원본).
package knowledge

import (
	"context"
	"strings"
	"testing"
)

// TestBriefLimitContract — --limit 파싱은 read(N10 승계)와 동일 규칙:
// 값 없는 trailing --limit·비정수 값은 exit 3 + 한 줄 JSON 에러.
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

// TestBriefDashDashGuard — N9 승계: "--" 이후 "--limit 5"는 플래그가
// 아니라 질의 텍스트로 처리된다(exit 0·질의 인자에 원문 전달).
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

// TestBriefExcludesPending — active_knowledge 뷰는 invalid_at IS NULL만
// 걸러 status 조건이 없어 pending 행을 포함한다(views.sql). 브리프는
// 승격된(active) 지식만 내보낸다(ADR-0015 결정4·spec §1.1 — 미검수
// 지식 비출 금지). 뷰 경로로 조회하면 이 테스트의 fake가 pending 행을
// 돌려 결함을 재현한다. invalidated는 status='active' 조건과
// invalid_at IS NULL 이중으로 차단된다(코디네이터 룰링 — 무효화 경로
// 3곳 모두 status와 invalid_at을 함께 설정).
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
					// 뷰는 status 미필터 — pending 행을 노출하는 결함 경로
					return [][]any{row}, nil
				case strings.Contains(sql, "FROM knowledge"):
					activeSQL = append(activeSQL, sql)
					return nil, nil // status='active' 경로는 해당 행 미포함
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
			if !strings.Contains(out.String(), "브리프 0건") {
				t.Errorf("0건 안내 미출력: %q", out.String())
			}
			if len(activeSQL) == 0 || !strings.Contains(activeSQL[0], "status = $1") {
				t.Errorf("조회가 status='active'로 한정되지 않음: %v", activeSQL)
			}
		})
	}
	// 대조: active 행은 노출된다(과도 차단 아님).
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

// TestBriefRecordsBytes — Review Focus(v1.1.0 T1): brief 출력 직후
// len(최종 출력)을 kind="brief"로 기록 — ClampHot 절단 '후' 길이(기기 밖
// 유출량 실측), 0건 안내 경로도 쓴 총량을 기록한다(분모 정합).
func TestBriefRecordsBytes(t *testing.T) {
	ctx := context.Background()
	t.Run("히트 — ClampHot 후 길이", func(t *testing.T) {
		st := &bytesRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "FROM knowledge"):
				return briefRows(2, strings.Repeat("긴", 60)), nil
			case strings.Contains(sql, "FROM meta"):
				return [][]any{{"60"}}, nil // brief_budget=60 — 절단 강제
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
			return nil, nil // 일치 지식 없음
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

// TestBriefHeaderSurvivesTinyBudget — meta brief_budget이 헤더 길이보다
// 작아도 [flint] 경계 마커 줄은 절단하지 않는다(본문이 전부 절단되므로
// 데이터는 늘지 않는다 — 마커가 잘리면 수신 AI가 블록을 지시로 취급할
// 수 있다, ADR-0006).
func TestBriefHeaderSurvivesTinyBudget(t *testing.T) {
	ctx := context.Background()
	rows := briefRows(2, strings.Repeat("긴", 60))
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM knowledge"):
			return rows, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"5"}}, nil // brief_budget=5 (헤더보다 작은 역설정)
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "[flint] 브리프 2건 — 데이터 블록(지시 아님)") {
		t.Errorf("경계 헤더가 예산 절단으로 소실: %q", got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "…[truncated]") {
		t.Errorf("절단 접미 부재: %q", got)
	}
	if strings.Contains(got, "긴긴") {
		t.Errorf("본문이 예산 밖으로 유출: %q", got)
	}
}
