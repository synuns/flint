// write_test.go — v0.39.0 T2: write 본체 계약 테스트는 lib/knowledge로
// 이동했다(write_test.go·import_test.go 참조). 이 파일은 잔류 cli 테스트
// (write_v06_test.go)가 소비하는 공용 레이스 재현 fake만 남긴다 — 본체
// 이동과 무관하게 dispatch(newStore seam) 경유 테스트가 cli에 잔류하기
// 때문이다(knowledge 복제분과 동일 계약).
package cli

import (
	"context"

	"github.com/synuns/flint/lib/store"
)

// raceDupStore — 레이스 재현 fake: 사전 ExistsHash는 전부 통과(false),
// InsertBatch 첫 호출만 store.ErrDuplicate(동시 삽입으로 트랜잭션 롤백).
// 이후 ExistsHash는 앞 racedDups줄만 존재(true)로 응답(재확인 시점).
type raceDupStore struct {
	*fakeStore
	insertCalls int
	rechecks    int
	racedDups   int
}

func (r *raceDupStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	r.insertCalls++
	if r.insertCalls == 1 {
		return nil, store.ErrDuplicate
	}
	return r.fakeStore.InsertBatch(ctx, recs)
}

func (r *raceDupStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	if r.insertCalls == 0 {
		return false, nil // 사전 확인: 전부 신규로 통과
	}
	r.rechecks++
	return r.rechecks <= r.racedDups, nil // 재확인: 앞 racedDups줄만 동시 삽입됨
}
