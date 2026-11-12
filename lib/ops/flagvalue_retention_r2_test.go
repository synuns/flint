// flagvalue_retention_r2_test.go — v1.0.2 R2 인자 검증 회귀 2종.
// (1) 값형 플래그가 뒤 플래그를 흡수하는 오타('--out --encrypt' → 경로
// "--encrypt")는 knowledge.ValidateFlagValue 공용 가드로 exit 3 — read
// --session 계약(v1.0.1 F3)과 대칭. (2) report 기본 --days 7도 보존창
// 상한(daily_retention_days) clamp — 오버라이드 <7일 환경의 빈 일자 행 방지.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
)

// guardDumpFake — 덤프 위임 fake: Dump 호출 자체를 기록(가드가 덤프
// 이전에 차단했는지 검증 — supply_test dumpDelegFake 관례의 가드 검증판).
type guardDumpFake struct {
	*fakeStore
	dumped bool
}

func (g *guardDumpFake) Dump(ctx context.Context, w io.Writer) error {
	g.dumped = true
	return errors.New("dump은 인자 검증 이후여야 한다")
}

// TestFlagAbsorptionExits3 — 플래그 흡수 값은 exit 3(무효 인자 계약).
// 표 구동: 커맨드×인자 — 정상 경로(대조행)는 통과해야 한다.
func TestFlagAbsorptionExits3(t *testing.T) {
	t.Run("backup --out --encrypt", func(t *testing.T) {
		st := &guardDumpFake{fakeStore: &fakeStore{}}
		var out strings.Builder
		code := RunBackup(context.Background(), st, []string{"--out", "--encrypt"}, nil, &out)
		if code != 3 {
			t.Fatalf("exit=%d, want 3 (출력 %s)", code, out.String())
		}
		if st.dumped {
			t.Error("가드 전에 덤프 실행 — 검증 순서 위반")
		}
	})
	t.Run("backup --out -x", func(t *testing.T) {
		st := &guardDumpFake{fakeStore: &fakeStore{}}
		var out strings.Builder
		code := RunBackup(context.Background(), st, []string{"--out", "-x"}, nil, &out)
		if code != 3 {
			t.Fatalf("exit=%d, want 3 (출력 %s)", code, out.String())
		}
	})
	t.Run("restore --file --drill", func(t *testing.T) {
		var out strings.Builder
		code := RunRestore(context.Background(), &fakeStore{}, []string{"--file", "--drill"}, nil, &out)
		if code != 3 {
			t.Fatalf("exit=%d, want 3 (출력 %s)", code, out.String())
		}
	})
	t.Run("report --out --html", func(t *testing.T) {
		t.Chdir(t.TempDir()) // 미가드 시 기록 시도하는 파일 격리
		var out strings.Builder
		code := RunReport(context.Background(), &fakeStore{}, []string{"--out", "--html"}, nil, &out)
		if code != 3 {
			t.Fatalf("exit=%d, want 3 (출력 %s)", code, out.String())
		}
	})
	t.Run("report --days --out", func(t *testing.T) {
		// --days 값의 플래그 흡수 — Atoi 실패 경로가 이미 exit 3(계약 행).
		var out strings.Builder
		code := RunReport(context.Background(), &fakeStore{}, []string{"--days", "--out"}, nil, &out)
		if code != 3 {
			t.Fatalf("exit=%d, want 3 (출력 %s)", code, out.String())
		}
	})
	t.Run("backup --out 정상 경로 통과", func(t *testing.T) {
		// 대조행 — '-' 접두가 아닌 경로는 가드가 막지 않는다(덤프는 seam 성공).
		withBackupSeams(t, "-- flint dump\n", nil, nil)
		path := t.TempDir() + "/ok.sql"
		var out strings.Builder
		code := RunBackup(context.Background(), &fakeStore{}, []string{"--out", path}, nil, &out)
		if code != 0 {
			t.Fatalf("정상 경로 exit=%d, want 0 (출력 %s)", code, out.String())
		}
	})
	t.Run("ValidateFlagValue 단위", func(t *testing.T) {
		if err := knowledge.ValidateFlagValue("out", "--encrypt"); err == nil {
			t.Error("플래그형 값 통과")
		}
		if err := knowledge.ValidateFlagValue("out", "-x"); err == nil {
			t.Error("단일 대시 값 통과")
		}
		if err := knowledge.ValidateFlagValue("out", "backup.sql"); err != nil {
			t.Errorf("정상 경로 거부: %v", err)
		}
	})
}

// retentionFake — thresholds(daily_retention_days)·daily:* 키를 응답하는
// fake(supplyFake 관례 — thresholds 쿼리가 "key IN"으로 daily:보다 먼저).
func retentionFake(retention string, daily map[string]string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM meta WHERE key IN"):
			if retention == "" {
				return nil, nil
			}
			return [][]any{{"daily_retention_days", retention}}, nil
		case strings.Contains(sql, "daily:"):
			rows := [][]any{}
			for k, v := range daily {
				rows = append(rows, []any{k, v})
			}
			return rows, nil
		case strings.Contains(sql, "read_stats"):
			return [][]any{{"read_stats", `{"calls":0}`}, {"hot_stats", `{"lens":[]}`}}, nil
		case strings.Contains(sql, "invalid_at IS NOT NULL"):
			return [][]any{{int64(0), int64(0), int64(0)}}, nil
		}
		return nil, nil
	}}
}

// reportDaysLen — RunReport 출력의 days 배열 길이.
func reportDaysLen(t *testing.T, args []string, retention string) int {
	t.Helper()
	var out strings.Builder
	code := RunReport(context.Background(), retentionFake(retention, nil), args, nil, &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	var m struct {
		Days []map[string]any `json:"days"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("JSON 아님: %v (%s)", err, out.String())
	}
	return len(m.Days)
}

// TestReportDefaultDaysClampedToRetention — 기본 --days 7도 보존창 상한
// clamp: retention 3 오버라이드 환경에서 기본(무인자)·명시(--days 5) 모두
// 3행. 미설정 환경은 기본 7 불변(clamp는 상한 초과 시에만).
func TestReportDefaultDaysClampedToRetention(t *testing.T) {
	if n := reportDaysLen(t, nil, "3"); n != 3 {
		t.Errorf("기본값+retention 3: days=%d행, want 3(기본 7이 상한 미적용)", n)
	}
	if n := reportDaysLen(t, []string{"--days", "5"}, "3"); n != 3 {
		t.Errorf("--days 5+retention 3: days=%d행, want 3(기존 상한 계약)", n)
	}
	if n := reportDaysLen(t, nil, ""); n != 7 {
		t.Errorf("기본값+미설정: days=%d행, want 7(기본 불변)", n)
	}
}
