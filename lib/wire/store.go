// Package wire — flint CLI 계약층(v0.38.0 §1). Store 인터페이스·디스패치
// Run·registry·DSN 해결·에러 한 줄 JSON·meta KV 접근을 소유한다.
// 커맨드 구현(lib/cli)은 공장 함수·Register로 이 층에 연결된다.
package wire

import "context"

// Store — 커맨드가 필요한 저장소 연산. 테스트는 fake으로 주입한다.
type Store interface {
	Migrate(ctx context.Context) error
	InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error)
	ExistsHash(ctx context.Context, hash string) (bool, error)
	ExistsProject(ctx context.Context, name string) (bool, error)
	Commit(ctx context.Context, msg string) (string, error)
	Ping(ctx context.Context) error
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
	Close()
}

// QueryOnce — reader 계열 재시도 1회 후 실패 전파(spec §7 읽기).
func QueryOnce(ctx context.Context, st Store, sql string, args []any) ([][]any, error) {
	rows, err := st.Query(ctx, sql, args...)
	if err != nil {
		rows, err = st.Query(ctx, sql, args...)
	}
	return rows, err
}
