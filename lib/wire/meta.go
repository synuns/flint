package wire

import (
	"context"
	"errors"

	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
)

func GetMetaKV(ctx context.Context, st Store, key string) (string, bool) {
	v, ok, _ := GetMetaKVErr(ctx, st, key)
	return v, ok
}

func GetMetaKVErr(ctx context.Context, st Store, key string) (string, bool, error) {
	rows, err := QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return "", false, err
	}
	return fmt.Sprint(rows[0][0]), true, nil
}

func SetMetaKV(ctx context.Context, st Store, key, value string) error {
	_, err := QueryOnce(ctx, st,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, []any{key, value})
	return err
}

func CompareSetMetaKV(ctx context.Context, st Store, key, oldVal, newVal string) (bool, error) {

	rows, err := st.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $3)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value
		 WHERE meta.value = $2
		 RETURNING key`, key, oldVal, newVal)
	if err != nil {

		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			return false, nil
		}
		return false, err
	}
	return len(rows) > 0, nil
}

func BumpMetaKV(ctx context.Context, st Store, key string) error {
	_, err := st.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '1')
		 ON CONFLICT (key) DO UPDATE SET value = CASE
		   WHEN meta.value ~ '^[0-9]+$' THEN (meta.value::bigint + 1)::text
		   ELSE '1' END`, key)
	return err
}
