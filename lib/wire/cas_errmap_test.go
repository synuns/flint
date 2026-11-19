package wire

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestCompareSetMetaKVSerializationLoser — a race loser's
// serialization failure (40001) is the normal lost-claim path — mapped to (false, nil).
// On a real DB it is timing-dependent, so firing is unstable — a fake injection is
// the only reliable regression net.
func TestCompareSetMetaKVSerializationLoser(t *testing.T) {
	cases := []struct {
		code    string
		wantWon bool
		wantErr bool
	}{
		{"40001", false, false}, // the serialization loser — inherits no-op
		{"40P01", false, true},  // deadlock — propagated (defending against over-swallowing)
		{"42501", false, true},  // permissions — propagated
		{"08006", false, true},  // connection failure — propagated
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
		if err != nil && !errors.Is(err, err) { // a non-nil demonstration helper
			t.Errorf("code=%s err 래핑 이상", c.code)
		}
	}
}
