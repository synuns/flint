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

type injectedStore struct{ wire.Store }

func (injectedStore) Close() {}

var (
	_ interface {
		Approve(ctx context.Context, id int, claim string) error
	} = injectedStore{}
	_ interface {
		Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error)
	} = injectedStore{}
	_ interface {
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
	_ interface {
		BumpReads(ctx context.Context, ids []int) error
	} = injectedStore{}
	_ interface {
		LogPlaybookUse(ctx context.Context, id int, ok bool) error
	} = injectedStore{}
	_ interface {
		UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error)
	} = injectedStore{}
	_ interface {
		StaleSessionRuns(ctx context.Context, agent string) (int, error)
	} = injectedStore{}
	_ ops.SessionStartRecorder = injectedStore{}
	_ interface {
		Dump(ctx context.Context, w io.Writer) error
	} = injectedStore{}
	_ interface {
		MetaDelete(ctx context.Context, keys []string) (int64, error)
	} = injectedStore{}
	_ ops.ConflictStore            = injectedStore{}
	_ ops.MaintStore               = injectedStore{}
	_ knowledge.TelemetryCommitter = injectedStore{}
)

func (s injectedStore) Approve(ctx context.Context, id int, claim string) error {
	if a, ok := s.Store.(interface {
		Approve(ctx context.Context, id int, claim string) error
	}); ok {
		return a.Approve(ctx, id, claim)
	}

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

	return 0, nil
}

func (s injectedStore) DupRowByHash(ctx context.Context, hash string) (knowledge.DupMeta, bool, error) {

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

func (s injectedStore) CommitTelemetry(ctx context.Context, cmd string) error {
	if tc, ok := s.Store.(knowledge.TelemetryCommitter); ok {
		return tc.CommitTelemetry(ctx, cmd)
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

func (s injectedStore) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	if us, ok := s.Store.(interface {
		UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error)
	}); ok {
		return us.UpdateSessionSummary(ctx, id, summary, tasks)
	}

	return 0, errors.New("session summary: unsupported store")
}

func (s injectedStore) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	if ss, ok := s.Store.(interface {
		StaleSessionRuns(ctx context.Context, agent string) (int, error)
	}); ok {
		return ss.StaleSessionRuns(ctx, agent)
	}
	return 0, nil
}

func (s injectedStore) Dump(ctx context.Context, w io.Writer) error {
	if ds, ok := s.Store.(interface {
		Dump(ctx context.Context, w io.Writer) error
	}); ok {
		return ds.Dump(ctx, w)
	}

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

func (s injectedStore) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	if ms, ok := s.Store.(ops.MaintStore); ok {
		return ms.PruneRecallEvents(ctx, before)
	}
	return 0, errors.New("maintain: unsupported store")
}

func (s injectedStore) Merge(ctx context.Context, fromID, toID int) error {
	if ms, ok := s.Store.(ops.MaintStore); ok {
		return ms.Merge(ctx, fromID, toID)
	}
	return errors.New("maintain: unsupported store")
}

func OpenServerStore() (wire.Store, error) {
	return newStore(context.Background(), wire.DSNFromEnv())
}

func RunWithStore(args []string, stdin io.Reader, stdout io.Writer, st wire.Store) int {
	if st == nil {
		return wire.Run(args, stdin, stdout, newStore)
	}
	return wire.Run(args, stdin, stdout, func(ctx context.Context, dsn string) (wire.Store, error) {
		return injectedStore{st}, nil
	})
}
