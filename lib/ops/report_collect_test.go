package ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

func TestCollectReportCollectsWindow(t *testing.T) {
	out, err := CollectReport(context.Background(), &fakeStore{}, 7)
	if err != nil {
		t.Fatalf("CollectReport: %v", err)
	}
	if len(out.Days) != 7 {
		t.Fatalf("days = %d, want 7", len(out.Days))
	}
}

func collectSeed() *fakeStore {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	evRows := make([][]any, 0, 10)
	for i := 0; i < 10; i++ {
		zeroReason := ""
		if i < 2 {
			zeroReason = "unowned"
		} else if i == 2 {
			zeroReason = "owned_miss"
		}
		evRows = append(evRows, []any{
			now.Add(time.Duration(i) * time.Minute), i < 3, i < 2,
			"1,2", i%3 + 1, 700, zeroReason, 1,
		})
	}
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "LIKE 'daily:%'"):
			return [][]any{
				{"daily:write:" + today, "100"},
				{"daily:dup:" + today, "9"},
				{"daily:read:" + today, "20"},
				{"daily:read_zero:" + today, "3"},
				{"daily:read_bytes:" + today, "4000"},
			}, nil
		case strings.Contains(sql, "length(summary) FROM knowledge"):
			return [][]any{{int64(1), int64(500)}}, nil
		case strings.Contains(sql, "near FROM read_events"):
			return [][]any{{now, false, true}}, nil
		case strings.Contains(sql, "fallback, ids"):
			return evRows, nil
		case strings.Contains(sql, "hot_events"):
			return [][]any{{"1,2"}}, nil
		case strings.Contains(sql, "kind = 'hot'"):
			return [][]any{{int64(3)}}, nil
		case strings.Contains(sql, "/* dead */"):
			return [][]any{{int64(5)}}, nil
		case strings.Contains(sql, "status = 'pending'"):
			return [][]any{{int64(1), int64(4), int64(1)}}, nil
		case strings.Contains(sql, "/* stale */"):
			return [][]any{{int64(2), int64(1)}}, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{int64(1)}}, nil
		}
		return nil, nil
	}}
}

func TestCollectReportParity(t *testing.T) {
	var out strings.Builder
	if code := RunReport(context.Background(), collectSeed(), []string{"--days", "7"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	var runOut ReportOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &runOut); err != nil {
		t.Fatalf("report JSON 파싱: %v (%s)", err, out.String())
	}

	colOut, err := CollectReport(context.Background(), collectSeed(), 7)
	if err != nil {
		t.Fatalf("CollectReport: %v", err)
	}

	if got, want := colOut.DupPct, runOut.DupPct; got != want {
		t.Errorf("dup_pct=%d, want %d(RunReport)", got, want)
	}
	if got, want := colOut.FallbackPct, runOut.FallbackPct; got != want {
		t.Errorf("fallback_pct=%d, want %d(RunReport)", got, want)
	}
	if got, want := colOut.Effect.Cost.SavingsLowerBoundBytes, runOut.Effect.Cost.SavingsLowerBoundBytes; got != want {
		t.Errorf("savings=%d, want %d(RunReport)", got, want)
	}

	runBytes, _ := json.Marshal(runOut)
	colBytes, _ := json.Marshal(colOut)
	if string(runBytes) != string(colBytes) {
		t.Errorf("ReportOutput 바이트 불일치\nrun : %s\ncollect: %s", runBytes, colBytes)
	}
}

func TestCollectReportPingFail(t *testing.T) {
	_, err := CollectReport(context.Background(), &fakeStore{pingErr: errors.New("engine down")}, 7)
	if err == nil {
		t.Fatal("Ping 실패 시 error 랩 필요")
	}
	if !strings.Contains(err.Error(), "engine down") {
		t.Errorf("원인 유실: %v", err)
	}
	if store.MapExit(err) != 1 && store.MapExit(err) != 2 {
		t.Errorf("MapExit=%d — 랩이 오류 체인을 끊음", store.MapExit(err))
	}
}

func TestCollectReportStoreErrorKeepsExit2Contract(t *testing.T) {
	_, err := CollectReport(context.Background(), &fakeStore{queryErr: errors.New("engine down")}, 30)
	if err == nil {
		t.Fatal("want error for broken store (exit 2 contract)")
	}
	if strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("stub not replaced: %v", err)
	}
}
