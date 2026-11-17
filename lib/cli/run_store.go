// run_store.go — v1.2.4 T1(B4): store 주입 실행 변형. flint mcp 장기
// 프로세스가 도구 호출마다 store를 다시 열지 않도록 RunWithStore로
// 서버 수명 store를 주입한다. 기존 cli.Run(root.go)은 무변 — nil 주입은
// wire.Run(newStore) 기존 경로와 동치다.
//
// v1.6.2 R33-3 F1: 래퍼는 wire.Store 메서드만 승격하므로 커맨드층의
// 선택 인터페이스 단언(lib/{knowledge,ops}의 approveStore·DupMetaStore·
// ReadEventStore 등 전수 grep 19종)이 전부 차단돼 approve 승격·duplicate
// 메타·read_events 계량이 상실됐다. 각 capability를 원본에 위임하는
// 메서드를 추가해 단언이 다시 통과하게 한다 — 미구현 원본은 소비자의
// 기존 폴백(에러 문자열·no-op·ExistsHash)과 동일하게 재현한다.
package cli

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

// injectedStore — 주입 store의 Close 차단 래퍼. wire.Run은 명령 완료 후
// st.Close()하는 단발 CLI 계약(v0.25.3 누수 패치)이라 서버 수명 store를
// 그대로 넘기면 첫 도구 호출에 닫힌다. 위임은 전부 원본, Close만 no-op.
type injectedStore struct{ wire.Store }

func (injectedStore) Close() {}

// 컴파일 타임 보증 — 래퍼가 아래 전수 capability를 전부 구현한다
// (익명 인터페이스는 knowledge·ops의 비내보내기 단언 대상과 동일
// 메서드 집합이다. 구조적 타이핑이라 패키지를 넘어 단언이 통과된다).
var (
	_ interface { // knowledge.approveStore
		Approve(ctx context.Context, id int) error
	} = injectedStore{}
	_ interface { // knowledge.supersedeStore
		Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error)
	} = injectedStore{}
	_ interface { // knowledge.supersedeRefStore
		SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error)
	} = injectedStore{}
	_ knowledge.DupMetaStore           = injectedStore{}
	_ knowledge.DupIDStore             = injectedStore{}
	_ knowledge.UsageStore             = injectedStore{}
	_ knowledge.UsageCountStore        = injectedStore{}
	_ knowledge.ReadEventStore         = injectedStore{}
	_ knowledge.BytesRecorder          = injectedStore{}
	_ knowledge.KnowledgeReadsRecorder = injectedStore{}
	_ knowledge.HotEventRecorder       = injectedStore{}
	_ interface {                      // knowledge.readBumper
		BumpReads(ctx context.Context, ids []int) error
	} = injectedStore{}
	_ interface { // knowledge.usageStore(done)
		LogPlaybookUse(ctx context.Context, id int, ok bool) error
	} = injectedStore{}
	_ interface { // knowledge.sessionSummaryStore
		UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error)
	} = injectedStore{}
	_ interface { // knowledge.staleSessionStore
		StaleSessionRuns(ctx context.Context, agent string) (int, error)
	} = injectedStore{}
	_ ops.SessionStartRecorder = injectedStore{}
	_ interface {              // ops.dumpStore
		Dump(ctx context.Context, w io.Writer) error
	} = injectedStore{}
	_ interface { // ops.metaDeleteStore
		MetaDelete(ctx context.Context, keys []string) (int64, error)
	} = injectedStore{}
	_ ops.ConflictStore = injectedStore{}
	_ ops.MaintStore    = injectedStore{}
)

// --- 승격·전이 (knowledge approve/import) ---

func (s injectedStore) Approve(ctx context.Context, id int) error {
	if a, ok := s.Store.(interface {
		Approve(ctx context.Context, id int) error
	}); ok {
		return a.Approve(ctx, id)
	}
	// 소비자 폴백(approve.go ApproveFn 기본값)과 동일 문구.
	return errors.New("approve: injected store does not implement Approve")
}

func (s injectedStore) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	if ss, ok := s.Store.(interface {
		Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error)
	}); ok {
		return ss.Supersede(ctx, oldID, newID, at)
	}
	return 0, errors.New("supersede: injected store does not implement Supersede")
}

func (s injectedStore) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	if rs, ok := s.Store.(interface {
		SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error)
	}); ok {
		return rs.SetSupersededRef(ctx, oldID, newID)
	}
	// 소비자 폴백은 건너뛰기(stderr 안내) — 0행으로 동등하게 스킵.
	return 0, nil
}

// --- dedup 메타 (knowledge write) ---

func (s injectedStore) DupRowByHash(ctx context.Context, hash string) (knowledge.DupMeta, bool, error) {
	// lookupHash 폴백 사슬(DupMetaStore→DupIDStore→ExistsHash)을 그대로
	// 재현 — 래퍼가 항상 DupMetaStore를 만족하므로 사슬 전체가 여기 있다.
	if ms, ok := s.Store.(knowledge.DupMetaStore); ok {
		return ms.DupRowByHash(ctx, hash)
	}
	if ds, ok := s.Store.(knowledge.DupIDStore); ok {
		id, exists, err := ds.DupIDByHash(ctx, hash)
		return knowledge.DupMeta{ID: id}, exists, err
	}
	exists, err := s.Store.ExistsHash(ctx, hash)
	return knowledge.DupMeta{}, exists, err
}

func (s injectedStore) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	if ds, ok := s.Store.(knowledge.DupIDStore); ok {
		return ds.DupIDByHash(ctx, hash)
	}
	exists, err := s.Store.ExistsHash(ctx, hash)
	return 0, exists, err
}

// --- 계측 (소비자가 오류를 조용히 무시 — 미구현 원본은 no-op) ---

func (s injectedStore) RecordUsage(ctx context.Context, kind, source string) error {
	if us, ok := s.Store.(knowledge.UsageStore); ok {
		return us.RecordUsage(ctx, kind, source)
	}
	return nil
}

func (s injectedStore) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	if cs, ok := s.Store.(knowledge.UsageCountStore); ok {
		return cs.RecordUsageCount(ctx, kind, source, count)
	}
	return nil
}

func (s injectedStore) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	if res, ok := s.Store.(knowledge.ReadEventStore); ok {
		return res.RecordReadEvent(ctx, ev)
	}
	return nil
}

func (s injectedStore) RecordBytes(ctx context.Context, kind string, n int) error {
	if br, ok := s.Store.(knowledge.BytesRecorder); ok {
		return br.RecordBytes(ctx, kind, n)
	}
	return nil
}

func (s injectedStore) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	if kr, ok := s.Store.(knowledge.KnowledgeReadsRecorder); ok {
		return kr.RecordKnowledgeReads(ctx, ids, session, kind)
	}
	return nil
}

func (s injectedStore) RecordHotEvent(ctx context.Context, ids string) error {
	if rec, ok := s.Store.(knowledge.HotEventRecorder); ok {
		return rec.RecordHotEvent(ctx, ids)
	}
	return nil
}

func (s injectedStore) BumpReads(ctx context.Context, ids []int) error {
	if rb, ok := s.Store.(interface {
		BumpReads(ctx context.Context, ids []int) error
	}); ok {
		return rb.BumpReads(ctx, ids)
	}
	return nil
}

func (s injectedStore) RecordSessionStart(ctx context.Context, session string) error {
	if rec, ok := s.Store.(ops.SessionStartRecorder); ok {
		return rec.RecordSessionStart(ctx, session)
	}
	return nil
}

func (s injectedStore) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	if us, ok2 := s.Store.(interface {
		LogPlaybookUse(ctx context.Context, id int, ok bool) error
	}); ok2 {
		return us.LogPlaybookUse(ctx, id, ok)
	}
	return nil
}

// --- 세션·유지보수 갱신 ---

func (s injectedStore) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	if us, ok := s.Store.(interface {
		UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error)
	}); ok {
		return us.UpdateSessionSummary(ctx, id, summary, tasks)
	}
	// 소비자 폴백은 stderr 1행 + 갱신 생략 — 오류 반환으로 동등하게 남긴다.
	return 0, errors.New("session summary: unsupported store")
}

func (s injectedStore) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	if ss, ok := s.Store.(interface {
		StaleSessionRuns(ctx context.Context, agent string) (int, error)
	}); ok {
		return ss.StaleSessionRuns(ctx, agent)
	}
	return 0, nil // 소비자 폴백: 경고 없는 0
}

// --- admin (ops backup·tuning·maintain·doctor) ---

func (s injectedStore) Dump(ctx context.Context, w io.Writer) error {
	if ds, ok := s.Store.(interface {
		Dump(ctx context.Context, w io.Writer) error
	}); ok {
		return ds.Dump(ctx, w)
	}
	// 소비자 폴백(ops.RunBackup DumpFn seam)과 동일 경로.
	return ops.DumpFn(ctx, wire.DSNFromEnv(), w)
}

func (s injectedStore) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	if md, ok := s.Store.(interface {
		MetaDelete(ctx context.Context, keys []string) (int64, error)
	}); ok {
		return md.MetaDelete(ctx, keys)
	}
	return 0, errors.New("tuning reset: unsupported store")
}

func (s injectedStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	if cs, ok := s.Store.(ops.ConflictStore); ok {
		return cs.ConflictGroups(ctx, limit)
	}
	return nil, errors.New("maintain: unsupported store")
}

func (s injectedStore) Counts(ctx context.Context) (db.Counts, error) {
	if ms, ok := s.Store.(ops.MaintStore); ok {
		return ms.Counts(ctx)
	}
	return db.Counts{}, errors.New("maintain: unsupported store")
}

func (s injectedStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	if ms, ok := s.Store.(ops.MaintStore); ok {
		return ms.DupGroups(ctx, limit)
	}
	return nil, errors.New("maintain: unsupported store")
}

func (s injectedStore) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	if ms, ok := s.Store.(ops.MaintStore); ok {
		return ms.Invalidate(ctx, ids, reason)
	}
	return 0, errors.New("maintain: unsupported store")
}

func (s injectedStore) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	if ms, ok := s.Store.(ops.MaintStore); ok {
		return ms.PruneInvalidated(ctx, before)
	}
	return 0, errors.New("maintain: unsupported store")
}

func (s injectedStore) Merge(ctx context.Context, fromID, toID int) error {
	if ms, ok := s.Store.(ops.MaintStore); ok {
		return ms.Merge(ctx, fromID, toID)
	}
	return errors.New("maintain: unsupported store")
}

// OpenServerStore — 서버 수명 store open(DSN 해결 포함). lib/mcp
// storeSupply 기본 실구현이 쓴다. 실패는 호출자가 nil 폴백으로 흡수
// (각 호출이 기존 Run 계약대로 알아서 실패한다).
func OpenServerStore() (wire.Store, error) {
	return newStore(context.Background(), wire.DSNFromEnv())
}

// RunWithStore — 주입 store로 커맨드를 실행한다. st가 nil이면 기존
// Run과 동일하게 내부 open(wire.Run + cli.newStore seam) — 출력·exit
// 계약 불변. 주입 시 연결 수명은 pgx pool에 위임(재연결 포함)하고
// Close만 차단해 서버 수명 재사용을 보장한다.
func RunWithStore(args []string, stdin io.Reader, stdout io.Writer, st wire.Store) int {
	if st == nil {
		return wire.Run(args, stdin, stdout, newStore)
	}
	return wire.Run(args, stdin, stdout, func(ctx context.Context, dsn string) (wire.Store, error) {
		return injectedStore{st}, nil
	})
}
