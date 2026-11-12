package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestZeroNearWarn(t *testing.T) {
	// v0.42.0 T2: zero-near 산출은 WindowedDaily(창 마킹) 경유 — 키 날짜가
	// 창 내(오늘)여야 합산된다(구 직접 meta 조회는 SQL이 창 컷을 담당).
	today := time.Now().UTC().Format("2006-01-02")
	c := checkZeroNear(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "daily:") {
			return [][]any{{"daily:" + today, `{"read":20,"read_zero_near":8}`}}, nil
		}
		return nil, nil
	}})
	if c.Status != "warn" {
		t.Errorf("40%% = %s, want warn", c.Status)
	}
}

func TestZeroNearSampleFloorOk(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	c := checkZeroNear(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "daily:") {
			return [][]any{{"daily:" + today, `{"read":5,"read_zero_near":3}`}}, nil
		}
		return nil, nil
	}})
	if c.Status != "ok" {
		t.Errorf("표본 5 = %s, want ok", c.Status)
	}
}
