// reads.go — T62 항목 참조 계측(schema v4). read 결과·hot 주입 항목의
// read_count·last_read_at 증분. 신규 인덱스 없음(spec §2 — status 인덱스
// 재사용, 개인 규모 시퀀셜 수용). 호출부(cli)는 실패를 조용히 무시한다.
package db

import (
	"context"
	"fmt"
)

// BumpReads는 ids에 해당하는 knowledge 행의 참조를 1씩 증분한다
//(read_count+1, last_read_at=now()). 빈 슬라이스는 no-op이고, 슬라이스 내
// 중복 id는 WHERE id=ANY의 행 매칭 기준이라 행당 1회만 반영된다.
func (r *Repo) BumpReads(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET read_count = read_count + 1, last_read_at = now()
		 WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("bump reads: %w", err)
	}
	return nil
}
