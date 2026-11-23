package query

import (
	"sort"
	"strconv"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/synuns/flint/lib/store"
)

type Suggestion struct {
	ID    int    `json:"id"`
	Topic string `json:"topic"`
}

func nfkc(s string) string { return norm.NFKC.String(s) }

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

func bigramSet(r []rune) map[string]struct{} {
	set := make(map[string]struct{}, len(r))
	for i := 0; i+1 < len(r); i++ {
		set[string(r[i:i+2])] = struct{}{}
	}
	return set
}

func BigramDice(a string, b string) float64 { return bigramDice(a, b) }

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

var suggestMinScore = store.DefaultThresholds().SuggestMinScore

func SetSuggestThresholds(minScore float64, scanCap int) {
	if minScore > 0 {
		suggestMinScore = minScore
	}
	if scanCap > 0 {
		suggestScanCap = scanCap
	}
}

func SuggestSimilar(q string, rows [][]any, topN int) []Suggestion {
	sug, _ := SuggestSimilarWithScanMax(q, rows, topN)
	return sug
}

var RecordSuggestScanFn func(time.Duration)

func SuggestSimilarWithScanMax(q string, rows [][]any, topN int) ([]Suggestion, float64) {

	start := time.Now()
	if RecordSuggestScanFn != nil {
		defer func() { RecordSuggestScanFn(time.Since(start)) }()
	}
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

var suggestScanCap = store.DefaultThresholds().SuggestScanCap

func BuildSuggestScan() (string, []any) {
	return "SELECT id, topic, summary FROM active_knowledge LIMIT " + strconv.Itoa(suggestScanCap), nil
}
