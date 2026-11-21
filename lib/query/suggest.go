// suggest.go — zero-hit did-you-mean suggestions (spec §3 R1, R2).
// Similarity and sorting are pure app-side functions — the phrase LIKE SQL and output are unchanged.
package query

import (
	"sort"
	"strconv"

	"golang.org/x/text/unicode/norm"

	"github.com/synuns/flint/lib/store"
)

// Suggestion — one suggestion (the id and topic of an active_knowledge row).
type Suggestion struct {
	ID    int    `json:"id"`
	Topic string `json:"topic"`
}

// nfkc — Unicode NFKC normalization of the matching comparison strings (R2).
// Blocks misses from NFC/NFD and composed-form notation differences. Applied only to the comparison strings — the originals, SQL, and output are unchanged.
func nfkc(s string) string { return norm.NFKC.String(s) }

// bigramDice — the Dice coefficient (0..1) of rune-bigram sets. For 1-rune
// strings with no bigrams on either side, an equality verdict (1/0). Empty strings and empty sets are 0.
func bigramDice(a string, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 && len(rb) < 2 {
		if len(ra) == 1 && len(rb) == 1 && ra[0] == rb[0] {
			return 1
		}
		return 0
	}
	sa, sb := bigramSet(ra), bigramSet(rb)
	if len(sa)+len(sb) == 0 {
		return 0
	}
	inter := 0
	for k := range sa {
		if _, ok := sb[k]; ok {
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(sa)+len(sb))
}

// bigramSet — the set of adjacent bigrams of a rune slice (deduplicated).
func bigramSet(r []rune) map[string]struct{} {
	set := make(map[string]struct{}, len(r))
	for i := 0; i+1 < len(r); i++ {
		set[string(r[i:i+2])] = struct{}{}
	}
	return set
}

// BigramDice — the public wrapper of the rune-bigram set Dice coefficient (the set version, bigramDice).
// Used when reusing the same measure as the suggestion score outside the package (cli, etc.).
func BigramDice(a string, b string) float64 { return bigramDice(a, b) }

// BigramDiceMultiset — the rune-bigram multiset Dice coefficient (the maintain
// contradiction-classification measure). Counts repeated bigrams by occurrence rather than
// flattening them like a set, yielding a more conservative similarity than the set version
// for sentences with repeated bigrams — the cli.conflictDupThreshold (0.6) ruling is based on
// this measure, so it is not merged into the set version. For short strings without bigrams
// (fewer than 2 runes), both-empty is 1 (identical), otherwise 0 (incomparable = dissimilar).
func BigramDiceMultiset(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 || len(rb) < 2 {
		if len(ra) == 0 && len(rb) == 0 {
			return 1
		}
		return 0
	}
	ma := map[string]int{}
	for i := 0; i+1 < len(ra); i++ {
		ma[string(ra[i:i+2])]++
	}
	mb := map[string]int{}
	for i := 0; i+1 < len(rb); i++ {
		mb[string(rb[i:i+2])]++
	}
	inter := 0
	for k, n := range ma {
		if m, ok := mb[k]; ok {
			if m < n {
				inter += m
			} else {
				inter += n
			}
		}
	}
	return 2 * float64(inter) / float64(len(ra)-1+len(rb)-1)
}

// suggestCellInt — the scan id cell (any) -> int. Accepts pgx numerics, float64,
// and decimal strings; anything else is (0, false) — a query-local clone with the same contract as cli.cellToInt.
func suggestCellInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	default:
		return 0, false
	}
}

// suggestMinScore — the suggestion adoption floor. Rows below this score are
// excluded as irrelevant candidates. The default uses the thresholds registry
// (store.DefaultThresholds) as the single source; at runtime, the cli read path
// injects the meta suggest_min_score override via SetSuggestThresholds.
// injects them.
var suggestMinScore = store.DefaultThresholds().SuggestMinScore

// SetSuggestThresholds — the suggestion-threshold (minScore, scanCap) injection seam.
// cli passes the loadThresholds result. Values <= 0 are ignored (double defense against unset fallbacks).
func SetSuggestThresholds(minScore float64, scanCap int) {
	if minScore > 0 {
		suggestMinScore = minScore
	}
	if scanCap > 0 {
		suggestScanCap = scanCap
	}
}

// SuggestSimilar — the top-N suggestions by NFKC-normalized bigram Dice similarity
// between the query q and the scanned rows ([0]=id, [1]=topic, [2]=summary).
// Rows below suggestMinScore (including 0) are excluded; descending, ties by ascending id. Fewer than topN are returned as-is.
// Delegates to SuggestSimilarWithScanMax (suggestions only) — the legacy
// signature and call sites are unchanged.
func SuggestSimilar(q string, rows [][]any, topN int) []Suggestion {
	sug, _ := SuggestSimilarWithScanMax(q, rows, topN)
	return sug
}

// SuggestSimilarWithScanMax — SuggestSimilar plus the scan max: the
// highest bigramDice score over ALL scanned candidate rows, computed
// BEFORE the suggestMinScore filter and independent of the top-N
// selection (schema v13 read_events.near_score source). A below-min
// zero row therefore still carries its best scan score — masking
// near=false rows to 0 would erase the min_score-boundary distribution
// (the counterfactual-analysis core). An empty scan yields 0 (nothing
// was scanned — distinct from a masked score); malformed rows (short·
// unparseable id) are not candidates and never feed the max.
func SuggestSimilarWithScanMax(q string, rows [][]any, topN int) ([]Suggestion, float64) {
	nq := nfkc(q)
	type scored struct {
		sug   Suggestion
		score float64
	}
	var cs []scored
	scanMax := 0.0
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		id, ok := suggestCellInt(row[0])
		if !ok {
			continue
		}
		topic, _ := row[1].(string)
		summary, _ := row[2].(string)
		score := bigramDice(nq, nfkc(topic+summary))
		if score > scanMax {
			scanMax = score
		}
		if score < suggestMinScore {
			continue
		}
		cs = append(cs, scored{Suggestion{ID: id, Topic: topic}, score})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].score != cs[j].score {
			return cs[i].score > cs[j].score
		}
		return cs[i].sug.ID < cs[j].sug.ID
	})
	if topN > 0 && len(cs) > topN {
		cs = cs[:topN]
	}
	if len(cs) == 0 {
		return nil, scanMax
	}
	out := make([]Suggestion, len(cs))
	for i, c := range cs {
		out[i] = c.sug
	}
	return out, scanMax
}

// suggestScanCap — the suggestion scan row cap (N13). Corrects spec §3 R1's 'no scan cap':
// personal knowledge-management scale (thousands of rows) is fully accepted, while
// defending the zero-hit path against unbounded scans at anomalous scale (mass
// intake, etc.). The cap is an unsorted defense line — since suggestions re-rank
// the top-N, whichever rows are caught still serve the purpose of providing near
// candidates. The default and override use the same seam as suggestMinScore (meta suggest_scan_cap).
var suggestScanCap = store.DefaultThresholds().SuggestScanCap

// BuildSuggestScan — the active_knowledge scan SQL for suggestions. No filters
// (suggestions target all active rows regardless of filters — spec §3 R1); the cap is suggestScanCap.
func BuildSuggestScan() (string, []any) {
	return "SELECT id, topic, summary FROM active_knowledge LIMIT " + strconv.Itoa(suggestScanCap), nil
}
