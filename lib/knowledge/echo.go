// echo.go — v1.17.0 T1: the echo scan engine. On every write, scans the
// active knowledge for near rows and reports them as echo candidates
// (duplicates / contradicts) so the writing agent can review its own
// consistency. Echo is suggestion only — verdicts (supersede, invalidate)
// stay with the agent via the existing approval circuit (§5, no
// self-reinforcing auto-supersede).
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

// EchoCandidate — one echo candidate: an active row the write echoes.
// Score is the rune-bigram multiset Dice (BigramDiceMultiset) of the
// combined texts — the same measure as the conflict classification, so a
// duplicates row is Score >= conflict_dup_threshold and a contradicts row
// is below it by construction.
type EchoCandidate struct {
	ID       int     `json:"id"`
	Topic    string  `json:"topic"`
	Score    float64 `json:"score"`
	Relation string  `json:"relation"` // duplicates | contradicts
}

// The echo relation labels.
const (
	EchoRelationDuplicates  = "duplicates"
	EchoRelationContradicts = "contradicts"
)

// The echo threshold defaults — single source until T3 registers the meta
// keys (echo_top_k, echo_min_score, echo_topic_match); until then these
// consts are the value basis, and the SetEchoThresholds seam is the
// runtime injection point (the suggest-threshold convention).
const (
	echoDefaultTopK       = 5
	echoDefaultMinScore   = 0.3 // raised vs suggest_min_score 0.15 — the write path tightens the gate to save scan cost
	echoDefaultTopicMatch = 0.4
)

// echoInputMaxRunes — the input cap of the combined comparison text
// (topic+" "+summary, 4096 runes; the suggest query bound inheritance).
const echoInputMaxRunes = 4096

// The runtime threshold seam (injected by the T2 write caller with the
// LoadThresholds values once the meta keys exist).
var (
	echoTopK       = echoDefaultTopK
	echoMinScore   = echoDefaultMinScore
	echoTopicMatch = echoDefaultTopicMatch
)

// SetEchoThresholds — the echo-threshold injection seam (the
// SetSuggestThresholds convention). Values <= 0 are ignored (double
// defense against unset fallbacks).
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

// The scan-bound seam vars (v1.17.1): the write path injects its already
// loaded SuggestScanCap·ConflictDupThreshold here so EchoScan does not
// re-query the meta thresholds (one write, one threshold load — the
// per-write duplicate LoadThresholds is abolished). <= 0 keeps the
// in-scan derivation (defaults + best-effort meta override) for callers
// that load nothing.
var (
	echoScanCap      int
	echoDupThreshold float64
)

// SetEchoScanBounds — the scan-bound injection seam (the SetEchoThresholds
// convention). Values <= 0 are ignored.
func SetEchoScanBounds(scanCap int, dupThreshold float64) {
	if scanCap > 0 {
		echoScanCap = scanCap
	}
	if dupThreshold > 0 {
		echoDupThreshold = dupThreshold
	}
}

// RecordEchoScanFn — the scan-duration recording seam (v1.21.0 A6 scan p95).
// ops wires its in-memory accumulator here (knowledge cannot import ops —
// the import-cycle-free seam var convention); nil (unwired) keeps the path
// metering-free and quiet (§13 — metering never changes the outcome).
var RecordEchoScanFn func(time.Duration)

// EchoScan — scans rows with invalid_at IS NULL (pending rows may appear
// as candidates — pre-existing since v1.17.0; merge-audit applies the
// strict active filter), bounded by the suggest_scan_cap row cap, and
// returns the top echoTopK echo candidates for the incoming topic+summary:
//
//  1. multiset Dice >= conflict_dup_threshold (0.6, the existing conflict
//     ruling) -> duplicates
//  2. multiset < 0.6 AND set Dice >= echoMinScore (0.3) AND topic-only
//     Dice >= echoTopicMatch (0.4) -> contradicts (a different claim on
//     the same topic)
//  3. below all gates -> excluded
//
// The comparison text is TruncateRunes(topic+" "+summary, 4096); the row
// side combines the same way (topic+" "+summary). Results sort by Score
// descending, ties by ascending id. A scan failure returns (nil, err) —
// the caller omits the echo field quietly (§13).
func EchoScan(ctx context.Context, st wire.Store, topic, summary string) ([]EchoCandidate, error) {
	// v1.21.0 A6: entry-to-return duration sample — in-memory only (zero
	// added queries; merged into meta scan_stats at the existing hot_stats
	// persist point). Recorded on every return path, error included.
	start := time.Now()
	if RecordEchoScanFn != nil {
		defer func() { RecordEchoScanFn(time.Since(start)) }()
	}
	// Scan bounds — the injected seam first (the write path passes its
	// already-loaded values, v1.17.1); a bound left unset (<=0) derives
	// from the meta table best effort, then the defaults (a lookup
	// failure falls back to the defaults, the maintain convention).
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
		// Re-sanitize stored values on output (the render choke point).
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
