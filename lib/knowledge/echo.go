package knowledge

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type EchoCandidate struct {
	ID       int     `json:"id"`
	Topic    string  `json:"topic"`
	Score    float64 `json:"score"`
	Relation string  `json:"relation"`
}

const (
	EchoRelationDuplicates  = "duplicates"
	EchoRelationContradicts = "contradicts"
)

const (
	echoDefaultTopK       = 5
	echoDefaultMinScore   = 0.3
	echoDefaultTopicMatch = 0.4
)

const echoInputMaxRunes = 4096

var (
	echoTopK       = echoDefaultTopK
	echoMinScore   = echoDefaultMinScore
	echoTopicMatch = echoDefaultTopicMatch
)

func SetEchoThresholds(topK int, minScore, topicMatch float64) {
	if topK > 0 {
		echoTopK = topK
	}
	if minScore > 0 {
		echoMinScore = minScore
	}
	if topicMatch > 0 {
		echoTopicMatch = topicMatch
	}
}

var (
	echoScanCap      int
	echoDupThreshold float64
)

func SetEchoScanBounds(scanCap int, dupThreshold float64) {
	if scanCap > 0 {
		echoScanCap = scanCap
	}
	if dupThreshold > 0 {
		echoDupThreshold = dupThreshold
	}
}

var RecordEchoScanFn func(time.Duration)

func EchoScan(ctx context.Context, st wire.Store, topic, summary string) ([]EchoCandidate, error) {

	start := time.Now()
	if RecordEchoScanFn != nil {
		defer func() { RecordEchoScanFn(time.Since(start)) }()
	}

	scanCap, dupThreshold := echoScanCap, echoDupThreshold
	if scanCap <= 0 || dupThreshold <= 0 {
		def := store.DefaultThresholds()
		if th, err := wire.LoadThresholds(ctx, st); err == nil {
			if scanCap <= 0 && th.SuggestScanCap > 0 {
				scanCap = th.SuggestScanCap
			}
			if dupThreshold <= 0 && th.ConflictDupThreshold > 0 {
				dupThreshold = th.ConflictDupThreshold
			}
		}
		if scanCap <= 0 {
			scanCap = def.SuggestScanCap
		}
		if dupThreshold <= 0 {
			dupThreshold = def.ConflictDupThreshold
		}
	}
	combined := render.TruncateRunes(topic+" "+summary, echoInputMaxRunes)
	rows, err := wire.QueryOnce(ctx, st,
		"SELECT id, topic, summary FROM knowledge WHERE invalid_at IS NULL LIMIT "+
			strconv.Itoa(scanCap), nil)
	if err != nil {
		return nil, err
	}
	var cs []EchoCandidate
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		id, ok := render.CellToInt(row[0])
		if !ok {
			continue
		}
		rowTopic, _ := row[1].(string)
		rowSummary, _ := row[2].(string)
		rowCombined := rowTopic + " " + rowSummary
		multi := query.BigramDiceMultiset(combined, rowCombined)
		relation := ""
		switch {
		case multi >= dupThreshold:
			relation = EchoRelationDuplicates
		case query.BigramDice(combined, rowCombined) >= echoMinScore &&
			query.BigramDice(topic, rowTopic) >= echoTopicMatch:
			relation = EchoRelationContradicts
		}
		if relation == "" {
			continue
		}

		cs = append(cs, EchoCandidate{ID: id, Topic: render.Text(rowTopic), Score: multi, Relation: relation})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Score != cs[j].Score {
			return cs[i].Score > cs[j].Score
		}
		return cs[i].ID < cs[j].ID
	})
	if echoTopK > 0 && len(cs) > echoTopK {
		cs = cs[:echoTopK]
	}
	return cs, nil
}
