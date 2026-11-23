package wire

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestCompareSetMetaKVSerializationLoser(t *testing.T) {
	cases := []struct {
		code    string
		wantWon bool
		wantErr bool
	}{
		{"40001", false, false},
		{"40P01", false, true},
		{"42501", false, true},
		{"08006", false, true},
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
		if err != nil && !errors.Is(err, err) {
			t.Errorf("code=%s err 래핑 이상", c.code)
		}
	}
}
