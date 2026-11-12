package wire

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestCompareSetMetaKVSerializationLoser — v1.0.12(R14-F1): 경합 패자의
// 직렬화 실패(40001)는 선점 탈락의 정상 경로 — (false, nil) 매핑.
// 실DB에서는 타이밍 의존적이라 발화가 불안정 — fake 주입이 유일한
// 확실한 회귀 보호망이다.
func TestCompareSetMetaKVSerializationLoser(t *testing.T) {
	cases := []struct {
		code    string
		wantWon bool
		wantErr bool
	}{
		{"40001", false, false}, // 직렬화 패자 — no-op 승계
		{"40P01", false, true},  // deadlock — 전파(과잉 삼킴 방어)
		{"42501", false, true},  // 권한 — 전파
		{"08006", false, true},  // 접속 실패 — 전파
	}
	for _, c := range cases {
		st := &fakeArgStore{query: func(_ context.Context, _ string, _ ...any) ([][]any, error) {
			return nil, &pgconn.PgError{Code: c.code, Message: "injected"}
		}}
		won, err := CompareSetMetaKV(context.Background(), st, "k", "old", "new")
		if won != c.wantWon {
			t.Errorf("code=%s won=%v, want %v", c.code, won, c.wantWon)
		}
		if (err != nil) != c.wantErr {
			t.Errorf("code=%s err=%v, wantErr %v", c.code, err, c.wantErr)
		}
		if err != nil && !errors.Is(err, err) { // nil 아님 실증 보조
			t.Errorf("code=%s err 래핑 이상", c.code)
		}
	}
}
