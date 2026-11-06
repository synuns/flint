// status_v0180_test.go — v0.18.0 R4: status 기간화 read 지표. 누적
// read_zero_pct(초기 이력 지배)와 별개로 14일 일별 합산 창 값을 병기한다.
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// windowedReadHarness — daily:* 질의에 3개 키(오늘·어제·창 밖 40일 전)를
// 반환하는 fake. 창 밖 합산 배제가 핵심 계약이므로 극단값(100/100)을 심는다.
func windowedReadHarness(t *testing.T) *fakeStore {
	t.Helper()
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	old := now.AddDate(0, 0, -40).Format("2006-01-02")
	daily := fmt.Sprintf(`{"read":10,"read_zero":4,"read_zero_near":1}`) // 오늘
	dailyY := fmt.Sprintf(`{"read":10,"read_zero":1}`)                  // 어제
	dailyOld := fmt.Sprintf(`{"read":100,"read_zero":100}`)             // 창 밖
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "LIKE 'daily:%'") {
			return [][]any{
				{"daily:" + today, daily},
				{"daily:" + yesterday, dailyY},
				{"daily:" + old, dailyOld},
			}, nil
		}
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"5"}}, nil // schema_version 호환값 — 파손 JSON 경로는 0 폴백
		}
		return [][]any{{int64(0)}}, nil
	}
	withFake(t, f)
	return f
}

// TestStatusWindowedRead — 창 내(14일) 일별 합산만: read=20·zero=5 →
// zero_pct 25. 40일 전 극단값(100/100)은 배제.
func TestStatusWindowedRead(t *testing.T) {
	f := windowedReadHarness(t)
	read, zeroPct, nearPct := statusWindowedRead(context.Background(), f, 14)
	if read != 20 {
		t.Fatalf("read=%d, want 20(창 내 합산)", read)
	}
	if zeroPct != 25 {
		t.Fatalf("zeroPct=%d, want 25(5/20)", zeroPct)
	}
	if nearPct != 5 {
		t.Fatalf("nearPct=%d, want 5(1/20)", nearPct)
	}
}

// TestStatusWindowedKeys — status 출력에 read_calls_14d·read_zero_pct_14d
// 키가 additive로 존재하고 창 합산 값이다(기존 키 불변).
func TestStatusWindowedKeys(t *testing.T) {
	windowedReadHarness(t)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if v, ok := m["read_calls_14d"].(float64); !ok || int(v) != 20 {
		t.Fatalf("read_calls_14d=%v, want 20", m["read_calls_14d"])
	}
	if v, ok := m["read_zero_pct_14d"].(float64); !ok || int(v) != 25 {
		t.Fatalf("read_zero_pct_14d=%v, want 25", m["read_zero_pct_14d"])
	}
	for _, legacy := range []string{"read_calls", "read_zero_pct", "pending", "schema_version"} {
		if _, ok := m[legacy]; !ok {
			t.Errorf("기존 키 %s 누락 — additive 원칙 위반", legacy)
		}
	}
}
