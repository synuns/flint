// thresholds_test.go — v0.39.0 T1 공용 헬퍼 wire 이동 검증. 기본 fake,
// SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시 poolStore로 실DB 경유
// (meta_kv_test.go seam 헬퍼 승계). Review Focus: TestLoadThresholdsFallback
// (미설정·조회 실패 시 DefaultThresholds 폴백 — 기존 cli 관례와 동일 결과).
package wire

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

// thresholdsFake — metaKVFake에 thresholds IN 조회 라우팅 추가.
// LoadThresholds의 `SELECT key, value FROM meta WHERE key IN (...)`를
// meta 맵에서 thresholds 키만 골라 응답한다(나머지는 임베디드 위임).
type thresholdsFake struct {
	metaKVFake
	failQuery bool
}

func (f *thresholdsFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if f.failQuery {
		return nil, context.DeadlineExceeded
	}
	if strings.Contains(sql, "SELECT key, value FROM meta WHERE key IN") {
		thKeys := map[string]bool{}
		for _, k := range ThresholdsKeys() {
			thKeys[k] = true
		}
		var keys []string
		for k := range f.meta {
			if thKeys[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys) // 결정적 순서(thresholdsKeySQL 관례)
		rows := make([][]any, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, []any{k, f.meta[k]})
		}
		return rows, nil
	}
	return f.metaKVFake.Query(ctx, sql, args...)
}

// thresholdsStore — 기본 thresholdsFake, SUPERLITE_REAL=1이면 poolStore.
func thresholdsStore(t *testing.T, f *thresholdsFake) Store {
	t.Helper()
	if dsn, real := seamRealDSN(); real {
		pool, err := db.Connect(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		s := &poolStore{pool: pool}
		t.Cleanup(s.Close)
		return s
	}
	return f
}

// TestLoadThresholdsFallback — Review Focus. 3계약:
//  1. meta 비었으면 전 키 DefaultThresholds(미설정 폴백 — ParseThresholds
//     규약 승계).
//  2. 저장 키는 오버라이드, '0' 저장 키도 기본값 폴백(미설정 취급).
//  3. 조회 실패는 err 전파 — 호출부(cli doctor 등)가 DefaultThresholds로
//     폴백하는 기존 관례를 그대로 승계한다.
func TestLoadThresholdsFallback(t *testing.T) {
	ctx := context.Background()
	def := store.DefaultThresholds()

	// 1. 미설정 — 전 키 기본값.
	f := &thresholdsFake{metaKVFake: metaKVFake{meta: map[string]string{}}}
	th, err := LoadThresholds(ctx, thresholdsStore(t, f))
	if err != nil {
		t.Fatalf("빈 meta 로드 err: %v", err)
	}
	if th.StaleDays != def.StaleDays || th.StallHotH != def.StallHotH || th.DrillMaxDays != def.DrillMaxDays {
		t.Fatalf("미설정 폴백 위반: stale_days=%d stall_hot_h=%d drill_max_days=%d", th.StaleDays, th.StallHotH, th.DrillMaxDays)
	}

	// 2. 오버라이드 반영 + '0'은 미설정 폴백.
	f2 := &thresholdsFake{metaKVFake: metaKVFake{meta: map[string]string{
		"stall_hot_h": "24",
		"stale_days":  "0",
	}}}
	st := thresholdsStore(t, f2)
	metaKVCleanup(t, st, "stall_hot_h", "stale_days")
	th2, err := LoadThresholds(ctx, st)
	if err != nil {
		t.Fatalf("오버라이드 로드 err: %v", err)
	}
	if th2.StallHotH != 24 {
		t.Fatalf("stall_hot_h=24 미반영: %d", th2.StallHotH)
	}
	if th2.StaleDays != def.StaleDays {
		t.Fatalf("'0' 저장 키 기본값 폴백 위반: %d", th2.StaleDays)
	}

	// 3. 조회 실패 — err 전파(호출부 기본값 폴백 관례 승계).
	f3 := &thresholdsFake{metaKVFake: metaKVFake{meta: map[string]string{}}, failQuery: true}
	if _, err := LoadThresholds(ctx, thresholdsStore(t, f3)); err == nil {
		t.Fatal("조회 실패 err 미전파 — 호출부 폴백 관례 깨짐")
	}
}

// (v0.42.0 T2 폐지 — TestBumpReadStatsToMeta: read_stats blob 폐지로
// LoadReadStats·BumpReadStatsToMeta가 사라졌다. 파생 경로 동등성은
// lib/ops/report_parity_test.go(TestEffectParity)가 담당한다.)

// (v1.1.0 T2 — TestReadSessionLabel·legacy 폴백 회귀는 lib/wire/
// session_test.go로 이동·갱신: 구 session-current 폴백 제거.)
