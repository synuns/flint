// realstore_read.go — v0.39.0 T3: read.go·hot.go가 lib/knowledge로 이동
// 하며 realStore 어댑터 메서드 4종(BumpReads·RecordReadUsage·
// RecordReadEvent·RecordHotEvent)만 cli에 추출. knowledge.readBumper·
// ReadUsageStore·ReadEventStore·HotEventRecorder 확장의 실구현 —
// realStore는 cli 소유 타입이라 knowledge에 둘 수 없다(사이클).
package cli

import (
	"context"

	"github.com/synuns/flint/lib/db"
)

// realStore.BumpReads — knowledge.readBumper 확장의 실구현
// (db.Repo.BumpReads 위임, T62 참조 계측).
func (s *realStore) BumpReads(ctx context.Context, ids []int) error {
	return s.repo.BumpReads(ctx, ids)
}

// realStore.RecordReadUsage — knowledge.ReadUsageStore 확장의 실구현
// (db.Repo.RecordReadUsage 위임, v0.15.0 R2).
func (s *realStore) RecordReadUsage(ctx context.Context, zero, near bool) error {
	return s.repo.RecordReadUsage(ctx, zero, near)
}

// realStore.RecordReadEvent — knowledge.ReadEventStore 확장의 실구현
// (db.Repo.RecordReadEvent 위임, v0.19.0 효과 계측). v0.42.0 T3:
// db.ReadEvent 구조체 1값 전달로 수렴.
func (s *realStore) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	return s.repo.RecordReadEvent(ctx, ev)
}

// realStore.RecordHotEvent — knowledge.HotEventRecorder 확장의 실구현
// (db.Repo.RecordHotEvent 위임, v0.20.0 E).
func (s *realStore) RecordHotEvent(ctx context.Context, ids string) error {
	return s.repo.RecordHotEvent(ctx, ids)
}
