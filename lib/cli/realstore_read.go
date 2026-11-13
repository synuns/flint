// realstore_read.go — v0.39.0 T3: read.go·hot.go가 lib/knowledge로 이동
// 하며 realStore 어댑터 메서드(BumpReads·RecordReadEvent·RecordHotEvent)만
// cli에 추출. knowledge.readBumper·ReadEventStore·HotEventRecorder 확장의
// 실구현 — realStore는 cli 소유 타입이라 knowledge에 둘 수 없다(사이클).
// v0.42.0 T2: RecordReadUsage 어댑터는 폐지(read_stats blob·daily read
// 쓰기 폐지 — read 계측 원천은 read_events 단일).
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

// realStore.RecordBytes — knowledge.BytesRecorder 확장의 실구현
// (db.Repo.RecordBytes 위임, v1.1.0 T1 G5 바이트 계측).
func (s *realStore) RecordBytes(ctx context.Context, kind string, n int) error {
	return s.repo.RecordBytes(ctx, kind, n)
}
