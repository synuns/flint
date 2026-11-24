package db

import (
	"context"

	"github.com/synuns/flint/lib/wire"
)

const MetaLookupSQL = "SELECT key, value FROM meta WHERE key IN ('hot_stats')"

const KnowledgeCountSQL = "SELECT id, length(summary) FROM knowledge"

const ReadEventsCountSQL = "SELECT count(*), count(CASE WHEN zero THEN 1 END) FROM read_events"

func MetaLookup(ctx context.Context, st wire.Store) ([][]any, error) {
	return wire.QueryOnce(ctx, st, MetaLookupSQL, nil)
}

func KnowledgeCount(ctx context.Context, st wire.Store) ([][]any, error) {
	return wire.QueryOnce(ctx, st, KnowledgeCountSQL, nil)
}

func ReadEventsCount(ctx context.Context, st wire.Store) ([][]any, error) {
	return wire.QueryOnce(ctx, st, ReadEventsCountSQL, nil)
}
