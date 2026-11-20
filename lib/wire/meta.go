// meta.go — the single entry point for meta-table key-value access. Consumed by the
// metrics metaKV, the tuning set, the setup exposure write, the hot lookup, and
// the read fallback increment (the output and exit contracts unchanged).
package wire

import (
	"context"
	"errors"

	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
)

// GetMetaKV — a single-key meta lookup. A missing key, an empty value, and a
// transport error all collapse to ("", false) (callers needing the error distinction use GetMetaKVErr).
func GetMetaKV(ctx context.Context, st Store, key string) (string, bool) {
	v, ok, _ := GetMetaKVErr(ctx, st, key)
	return v, ok
}

// GetMetaKVErr — GetMetaKV's prototype: returns (value, exists, transport
// error) as-is. The metering paths (Bump*) skip the write on error so as not
// to wipe the existing value — the err distinction is the contract.
func GetMetaKVErr(ctx context.Context, st Store, key string) (string, bool, error) {
	rows, err := QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return "", false, err
	}
	return fmt.Sprint(rows[0][0]), true, nil
}

// SetMetaKV — the single ON CONFLICT atomic-upsert implementation.
func SetMetaKV(ctx context.Context, st Store, key, value string) error {
	_, err := QueryOnce(ctx, st,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, []any{key, value})
	return err
}

// CompareSetMetaKV — an atomic conditional update (CAS): if the current value is
// old, change to new and return true, else false (parallel-session resource
// claiming). A single upsert-WHERE statement atomically handles both the absent
// (first) and the matching-existing cases. The same contract as BumpMetaKV:
// a conditional write is not safe to retry on ambiguous failure, so it runs as a single st.Query with no retry.
func CompareSetMetaKV(ctx context.Context, st Store, key, oldVal, newVal string) (bool, error) {
	// the arguments are passed spread — passing a single slice to the variadic
	// Query would make pgx reject it with "expected 3, got 1", breaking CAS
	// entirely (demonstrated on a real DB — fake claim-injection tests could not
	rows, err := st.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $3)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value
		 WHERE meta.value = $2
		 RETURNING key`, key, oldVal, newVal)
	if err != nil {
		// a race loser's serialization failure (Doltgres 40001) is the
		// normal lost-claim path — mapped to won=false, keeping the parallel no-op
		// contract (a real-DB 8-way run: the 7 losers used to return as errors, now no-ops).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			return false, nil
		}
		return false, err
	}
	return len(rows) > 0, nil
}

// BumpMetaKV — a single atomic increment of a counter key. Computes within a
// single upsert, without the non-atomic SELECT->INSERT round trip. If the
// existing value is not a positive-integer notation (absent or corrupt), it
// resets to 1 — the same result as the existing read_fallback_calls fallback.
// Contract: the increment is non-idempotent, so no retry — using QueryOnce
// (1 rerun on error) would double-count on an ambiguous failure (executed on
// the server, only the response lost). It runs as a single st.Query and
// propagates the failure as-is (the caller absorbs it per the metering principle — see done.go, read.go).
func BumpMetaKV(ctx context.Context, st Store, key string) error {
	_, err := st.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '1')
		 ON CONFLICT (key) DO UPDATE SET value = CASE
		   WHEN meta.value ~ '^[0-9]+$' THEN (meta.value::bigint + 1)::text
		   ELSE '1' END`, key)
	return err
}
