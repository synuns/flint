// usage_wire.go — T61: realStore 위임으로 db.Repo.RecordUsage(T60)를 cli
// 계측(recordUsage의 UsageStore 단언)에 연결한다. 메서드 정의만 여기 둔다
// (maintain.go의 MaintStore 위임 패턴과 동일).
package cli

import "context"

// RecordUsage — realStore 위임: 유입 계측 1회 기록(kind·source는 T60 계약).
func (s *realStore) RecordUsage(ctx context.Context, kind, source string) error {
	return s.repo.RecordUsage(ctx, kind, source)
}

var _ UsageStore = (*realStore)(nil)
