package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunPendingVerdicts(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	old, recent := time.Now().UTC().Add(-20*24*time.Hour), time.Now().UTC().Add(-24*time.Hour)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{
				{int32(1), "오래된 항목", "본문이 충분히 길다 — 만료 판정 대상", "agent", old},
				{int32(2), "최근 항목", "본문이 충분히 길어서 승격 판정 대상이 되는 최소 길이를 넘기는 예시 문장이다", "hook", recent},
				{int32(3), "짧은 최근", "짧다", "hook", recent},
			}, nil
		}
		if strings.Contains(sql, "status = 'active'") {
			return nil, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	code := RunPending(context.Background(), st, nil, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"1|expire|", "2|promote|", "3|hold|",
		`"expire":1`, `"promote":1`, `"hold":1`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("출력에 %q 부재:\n%s", want, s)
		}
	}
}

func TestRunPendingMergeHint(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	recent := time.Now().UTC().Add(-24 * time.Hour)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{{int32(9), "표 파이프 이스케이프 규칙(합성 데모)", "유사 active가 있는 최신 항목 — 병합 추천 대상", "agent", recent}}, nil
		}
		if strings.Contains(sql, "status = 'active'") {

			return [][]any{{int32(4), "표 파이프 이스케이프 규칙(합성 데모) 기존 활성"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunPending(context.Background(), st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "9|merge|") || !strings.Contains(out.String(), `"merge":1`) {
		t.Errorf("병합 추천 미출력:\n%s", out.String())
	}
}

func TestRunPendingJSONCountsConsistency(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{{int32(11), "파손 시각 행", "본문이 충분히 긴 합성 요약 행입니다", "agent", "not-a-time"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunPending(context.Background(), st, []string{"--json"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	s := out.String()
	if strings.Contains(s, `"expire":1`) {
		t.Errorf("counts 모순 — 파손 행이 expire로 집계: %s", s)
	}
	if !strings.Contains(s, `"hold":1`) || !strings.Contains(s, `"verdict":"hold"`) {
		t.Errorf("양쪽 hold 일치 없음: %s", s)
	}

	if !strings.Contains(s, `"age_days":0`) {
		t.Errorf("zero valid_at의 age_days 비정상: %s", s)
	}
	if strings.Contains(s, `"items":null`) {
		t.Errorf("items null: %s", s)
	}
}
