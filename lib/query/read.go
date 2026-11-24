package query

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type Filters struct {
	Tag    string
	Entity string
	Domain string
	Limit  int

	Status string

	MinConfidence *float64

	Origin string
}

const (
	defaultLimit = 10
	maxLimit     = 50
	summaryMax   = 80

	ReadFallbackCountKey = "read_fallback_calls"

	maxQueryTokens = 8
)

func ClampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

var readColumns = []string{
	"id", "topic", "summary", "tags", "entity", "domain", "confidence", "status",
	"read_count",
}

func tokenizeQuery(q string) []string {
	fields := strings.Fields(q)
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		f = trimPunct(f)
		if f != "" {
			tokens = append(tokens, f)
		}
		if len(tokens) == maxQueryTokens {
			break
		}
	}
	return tokens
}

func TokenizeQuery(q string) []string { return tokenizeQuery(q) }

func trimPunct(s string) string {
	r := []rune(s)
	for len(r) > 0 && unicode.IsPunct(r[0]) {
		r = r[1:]
	}
	for len(r) > 0 && unicode.IsPunct(r[len(r)-1]) {
		r = r[:len(r)-1]
	}
	return string(r)
}

func fallbackMatchCount(tokens []string, topic string, summary string) int {
	t, s := nfkc(strings.ToLower(topic)), nfkc(strings.ToLower(summary))
	n := 0
	for _, tok := range tokens {
		tok = nfkc(strings.ToLower(tok))
		if strings.Contains(t, tok) || strings.Contains(s, tok) {
			n++
		}
	}
	return n
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func EscapeLike(s string) string { return likeEscaper.Replace(s) }

func RankOrder(placeholder int) string {
	return fmt.Sprintf("ORDER BY (lower(topic)=lower($%d)) DESC, confidence DESC, ingested_at DESC, last_read_at DESC NULLS LAST, read_count DESC",
		placeholder)
}

func appendReadFilters(where []string, args []any, q string, f Filters) ([]string, []any) {
	if q != "" {
		args = append(args, "%"+likeEscaper.Replace(q)+"%")
		where = append(where, fmt.Sprintf("lower(topic || ' ' || summary) LIKE lower($%d)", len(args)))
	}
	if f.Tag != "" {
		args = append(args, f.Tag)

		where = append(where, fmt.Sprintf("strpos(tags, ':' || $%d || ':') > 0", len(args)))
	}
	if f.Entity != "" {
		args = append(args, f.Entity)
		where = append(where, fmt.Sprintf("entity = $%d", len(args)))
	}
	if f.Domain != "" {
		args = append(args, f.Domain)
		where = append(where, fmt.Sprintf("domain = $%d", len(args)))
	}
	if f.MinConfidence != nil {
		args = append(args, *f.MinConfidence)
		where = append(where, fmt.Sprintf("confidence >= $%d", len(args)))
	}
	if f.Origin != "" {
		args = append(args, f.Origin)
		where = append(where, fmt.Sprintf("origin = $%d", len(args)))
	}
	return where, args
}

func BuildRead(q string, f Filters) (string, []any) {
	var where []string
	var args []any

	where, args = appendReadFilters(where, args, q, f)

	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM active_knowledge"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, q)
	sql += " " + RankOrder(len(args))
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args
}

func BuildReadStatus(q string, f Filters) (string, []any) {
	var where []string
	var args []any

	args = append(args, f.Status)
	where = append(where, fmt.Sprintf("status = $%d", len(args)))
	if f.Status == "active" {
		where = append(where, "invalid_at IS NULL")
	}
	where, args = appendReadFilters(where, args, q, f)

	args = append(args, q)
	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM knowledge WHERE " +
		strings.Join(where, " AND ") + " " + RankOrder(len(args))
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args
}

func BuildReadFallback(q string, f Filters) (string, []any, bool) {
	tokens := tokenizeQuery(q)
	if len(tokens) < 2 {
		return "", nil, false
	}
	var conds []string
	var args []any
	for _, tok := range tokens {
		args = append(args, "%"+likeEscaper.Replace(tok)+"%")

		conds = append(conds, fmt.Sprintf("lower(topic || ' ' || summary || ' ' || tags) LIKE lower($%d)", len(args)))
	}
	where := []string{"(" + strings.Join(conds, " OR ") + ")"}
	if f.Tag != "" {
		args = append(args, f.Tag)
		where = append(where, fmt.Sprintf("strpos(tags, ':' || $%d || ':') > 0", len(args)))
	}
	if f.Entity != "" {
		args = append(args, f.Entity)
		where = append(where, fmt.Sprintf("entity = $%d", len(args)))
	}
	if f.Domain != "" {
		args = append(args, f.Domain)
		where = append(where, fmt.Sprintf("domain = $%d", len(args)))
	}

	if f.MinConfidence != nil {
		args = append(args, *f.MinConfidence)
		where = append(where, fmt.Sprintf("confidence >= $%d", len(args)))
	}
	if f.Origin != "" {
		args = append(args, f.Origin)
		where = append(where, fmt.Sprintf("origin = $%d", len(args)))
	}
	args = append(args, q)
	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM active_knowledge WHERE " +
		strings.Join(where, " AND ") + " " + RankOrder(len(args))
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args, true
}

var rrfK = 60.0

func SetRRFK(k float64) {
	if k > 0 {
		rrfK = k
	}
}

func ReorderByFallbackCount(rows [][]any, tokens []string) {
	if len(rows) < 2 {
		return
	}
	values := rrfSignalValues(rows, tokens)
	scores := rrfFuse(values)
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if scores[idx[a]] != scores[idx[b]] {
			return scores[idx[a]] > scores[idx[b]]
		}
		return idx[a] < idx[b]
	})
	out := make([][]any, len(rows))
	for pos, src := range idx {
		out[pos] = rows[src]
	}
	copy(rows, out)
}

func rrfSignalValues(rows [][]any, tokens []string) [][]int {
	tokenJamo := jamoTokenSets(tokens)
	values := make([][]int, len(rows))
	for i, row := range rows {
		topic, summary := "", ""
		if idx := readColumnIndexOf("topic"); idx >= 0 && idx < len(row) {
			topic, _ = row[idx].(string)
		}
		if idx := readColumnIndexOf("summary"); idx >= 0 && idx < len(row) {
			summary, _ = row[idx].(string)
		}

		t := nfkc(strings.ToLower(topic))
		sm := nfkc(strings.ToLower(summary))
		values[i] = []int{
			fallbackMatchCount(tokens, t, sm),
			readCountOf(row),
			jamoOverlap(tokenJamo, t, sm),
			topicMatchCount(tokens, t),
		}
	}
	return values
}

const (
	tokenTopicPrefixScore    = 4
	tokenSummaryPrefixScore  = 3
	tokenTopicContainScore   = 2
	tokenSummaryContainScore = 1
)

func normalizeTokenText(s string) string {
	return strings.Join(strings.Fields(nfkc(strings.ToLower(s))), " ")
}

func cellString(row []any, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	if s, ok := row[idx].(string); ok {
		return s
	}
	return ""
}

func tokenTierScore(tok, topic, summary string) int {
	switch {
	case strings.HasPrefix(topic, tok):
		return tokenTopicPrefixScore
	case strings.HasPrefix(summary, tok):
		return tokenSummaryPrefixScore
	case strings.Contains(topic, tok):
		return tokenTopicContainScore
	case strings.Contains(summary, tok):
		return tokenSummaryContainScore
	}
	return 0
}

func tokenTierName(tier int) string {
	switch tier {
	case tokenTopicPrefixScore:
		return "topic_prefix"
	case tokenSummaryPrefixScore:
		return "summary_prefix"
	case tokenTopicContainScore:
		return "topic_contain"
	case tokenSummaryContainScore:
		return "summary_contain"
	}
	return ""
}

func tokenRowBreakdown(normTokens []string, topic, summary string) (parts []string, cover, total int) {
	for _, tok := range normTokens {
		if tok == "" {
			continue
		}
		if tier := tokenTierScore(tok, topic, summary); tier > 0 {
			parts = append(parts, tokenTierName(tier)+"×"+strconv.Itoa(tier))
			cover++
			total += tier + 1
		}
	}
	return parts, cover, total
}

func ReorderByTokenScore(rows [][]any, q string, colIdx map[string]int) [][]any {
	tokens := tokenizeQuery(q)
	if len(tokens) == 0 || len(rows) < 2 {
		return rows
	}
	normTokens := make([]string, len(tokens))
	for i, tok := range tokens {
		normTokens[i] = normalizeTokenText(tok)
	}

	topicIdx, summaryIdx := -1, -1
	if v, ok := colIdx["topic"]; ok {
		topicIdx = v
	}
	if v, ok := colIdx["summary"]; ok {
		summaryIdx = v
	}
	scores := make([]int, len(rows))
	for i, row := range rows {
		t := normalizeTokenText(cellString(row, topicIdx))
		s := normalizeTokenText(cellString(row, summaryIdx))
		_, _, total := tokenRowBreakdown(normTokens, t, s)
		scores[i] = total
	}
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return scores[idx[a]] > scores[idx[b]]
	})
	out := make([][]any, len(rows))
	for pos, src := range idx {
		out[pos] = rows[src]
	}
	return out
}

func ReadColumnIndexMap() map[string]int {
	m := make(map[string]int, len(readColumns))
	for i, c := range readColumns {
		m[c] = i
	}
	return m
}

func rrfRanks(values [][]int) [][]int {
	n := len(values)
	if n == 0 {
		return nil
	}
	ranks := make([][]int, n)
	for i := range ranks {
		ranks[i] = make([]int, len(values[0]))
	}
	for sig := range values[0] {
		order := make([]int, n)
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			return values[order[a]][sig] > values[order[b]][sig]
		})
		rank := 1
		for pos, row := range order {
			if pos > 0 && values[row][sig] != values[order[pos-1]][sig] {
				rank = pos + 1
			}
			if values[row][sig] > 0 {
				ranks[row][sig] = rank
			}
		}
	}
	return ranks
}

func rrfFuse(values [][]int) []float64 {
	n := len(values)
	if n == 0 {
		return nil
	}
	ranks := rrfRanks(values)
	scores := make([]float64, n)
	for row := range scores {
		for sig := range ranks[row] {
			if ranks[row][sig] > 0 {
				scores[row] += 1.0 / (rrfK + float64(ranks[row][sig]))
			}
		}
	}
	return scores
}

func topicMatchCount(tokens []string, t string) int {
	n := 0
	for _, tok := range tokens {
		if strings.Contains(t, nfkc(strings.ToLower(tok))) {
			n++
		}
	}
	return n
}

func jamoOverlap(tokenSets []map[string]struct{}, t, s string) int {
	if len(tokenSets) == 0 {
		return 0
	}
	hay := jamoBigramSet(nfkc(strings.ToLower(t + " " + s)))
	n := 0
	for _, set := range tokenSets {
		for bg := range set {
			if _, ok := hay[bg]; ok {
				n++
				break
			}
		}
	}
	return n
}

func jamoTokenSets(tokens []string) []map[string]struct{} {
	var out []map[string]struct{}
	for _, tok := range tokens {

		n := nfkc(strings.ToLower(tok))
		if containsHangul(n) {
			out = append(out, jamoBigramSet(n))
		}
	}
	return out
}

func containsHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}

func jamoBigramSet(s string) map[string]struct{} {
	const (
		syllableBase = 0xAC00
		leadBase     = 0x1100
		vowelBase    = 0x1161
		trailBase    = 0x11A7
	)
	var jamo []rune
	for _, r := range s {
		if r >= syllableBase && r <= 0xD7A3 {
			off := r - syllableBase
			jamo = append(jamo, leadBase+off/588, vowelBase+(off%588)/28)
			if t := off % 28; t > 0 {
				jamo = append(jamo, trailBase+t)
			}
		} else {
			jamo = append(jamo, r)
		}
	}
	return bigramSet(jamo)
}

func readColumnIndexOf(name string) int {
	for i, c := range readColumns {
		if c == name {
			return i
		}
	}
	return -1
}

func readCountOf(row []any) int {
	i := readColumnIndexOf("read_count")
	if i < 0 || i >= len(row) {
		return 0
	}
	switch n := row[i].(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func ReadFieldKnown(name string) bool {
	for _, c := range readColumns {
		if c == name {
			return true
		}
	}
	return false
}

func ReadColumnsHint() string {
	return strings.Join(readColumns, ",")
}

const VaultMaxRows = 5

const vaultTitleMax = 200

var vaultColumns = []string{"title", "summary", "tags", "path"}

func BuildVaultRead(q string) (string, []any) {
	args := []any{"%" + likeEscaper.Replace(q) + "%"}
	sql := "SELECT " + strings.Join(vaultColumns, ", ") + " FROM vault_files" +
		fmt.Sprintf(" WHERE lower(title || ' ' || summary || ' ' || tags) LIKE lower($%d)", len(args)) +
		" ORDER BY path LIMIT " + strconv.Itoa(VaultMaxRows)
	return sql, args
}

type VaultLine struct {
	Source  string `json:"source"`
	Path    string `json:"path"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

func FormatVaultRows(rows [][]any, jsonMode bool) string {

	const (
		vaultTitleIdx   = 0
		vaultSummaryIdx = 1
		vaultPathIdx    = 3
	)
	var lines []string
	for _, row := range rows {
		val := func(i int) string {
			if i < 0 || i >= len(row) || row[i] == nil {
				return ""
			}
			return fmt.Sprint(row[i])
		}
		if jsonMode {
			b, err := json.Marshal(VaultLine{
				Source:  "vault",
				Path:    val(vaultPathIdx),
				Title:   truncateRunes(val(vaultTitleIdx), vaultTitleMax),
				Summary: truncateRunes(val(vaultSummaryIdx), summaryMax),
			})
			if err != nil {
				continue
			}
			lines = append(lines, string(b))
		} else {
			lines = append(lines, strings.Join([]string{
				"vault",
				truncateRunes(noPipe(val(vaultPathIdx)), vaultTitleMax),
				truncateRunes(noPipe(val(vaultTitleIdx)), vaultTitleMax),
				truncateRunes(noPipe(val(vaultSummaryIdx)), summaryMax),
			}, "|"))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func noPipe(s string) string {
	return strings.ReplaceAll(s, "|", " ")
}

func FormatProvenanceSuffix(source string, confidence float64) string {
	prov := ""
	if source != "" {
		prov += "|source=" + noPipe(source)
	}
	if confidence != 0 {
		prov += "|confidence=" + strconv.FormatFloat(confidence, 'g', -1, 64)
	}
	return prov
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

const RelatedMaxRows = VaultMaxRows

func BuildRelatedRead(id int) (string, []any) {
	sql := "SELECT k.id, k.topic, k.summary, l.relation, 'forward' AS direction" +
		" FROM knowledge_links l JOIN active_knowledge k ON k.id = l.to_id::int" +
		" WHERE l.from_id = $1" +
		" UNION ALL " +
		"SELECT k.id, k.topic, k.summary, l.relation, 'reverse' AS direction" +
		" FROM knowledge_links l JOIN active_knowledge k ON k.id = l.from_id::int" +
		" WHERE l.to_id = $1" +
		" ORDER BY id LIMIT " + strconv.Itoa(RelatedMaxRows)
	return sql, []any{id}
}

type RelatedLine struct {
	Source    string `json:"source"`
	ID        string `json:"id"`
	Relation  string `json:"relation"`
	Direction string `json:"direction"`
	Topic     string `json:"topic"`
	Summary   string `json:"summary"`
}

func FormatRelatedRows(rows [][]any, jsonMode bool) string {

	const (
		relatedIDIdx    = 0
		relatedTopicIdx = 1
		relatedSumIdx   = 2
		relatedRelIdx   = 3
		relatedDirIdx   = 4
	)
	var lines []string
	for _, row := range rows {
		val := func(i int) string {
			if i < 0 || i >= len(row) || row[i] == nil {
				return ""
			}
			return fmt.Sprint(row[i])
		}
		if jsonMode {
			b, err := json.Marshal(RelatedLine{
				Source:    "related",
				ID:        val(relatedIDIdx),
				Relation:  truncateRunes(noPipe(val(relatedRelIdx)), 64),
				Direction: truncateRunes(noPipe(val(relatedDirIdx)), 16),
				Topic:     truncateRunes(val(relatedTopicIdx), vaultTitleMax),
				Summary:   truncateRunes(val(relatedSumIdx), summaryMax),
			})
			if err != nil {
				continue
			}
			lines = append(lines, string(b))
		} else {
			lines = append(lines, strings.Join([]string{
				"related",
				truncateRunes(noPipe(val(relatedIDIdx)), 32),
				truncateRunes(noPipe(val(relatedRelIdx)), 64),
				truncateRunes(noPipe(val(relatedDirIdx)), 16),
				truncateRunes(noPipe(val(relatedTopicIdx)), vaultTitleMax),
				truncateRunes(noPipe(val(relatedSumIdx)), summaryMax),
			}, "|"))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

const WhyMaxRows = defaultLimit

type RRFSignalEvidence struct {
	Name    string  `json:"signal"`
	Value   int     `json:"value"`
	Rank    int     `json:"rank"`
	Contrib float64 `json:"contrib"`
}

type WhyEvidence struct {
	ID         string              `json:"id"`
	Tokens     string              `json:"tokens,omitempty"`
	TokenScore int                 `json:"token_score,omitempty"`
	RRF        []RRFSignalEvidence `json:"rrf,omitempty"`
	RRFScore   float64             `json:"rrf_score,omitempty"`
	Source     string              `json:"source,omitempty"`
	Confidence float64             `json:"confidence,omitempty"`
}

var rrfSignalNames = []string{"match", "read_count", "jamo", "topic"}

func whyRowID(row []any) string {
	i := readColumnIndexOf("id")
	if i < 0 || i >= len(row) || row[i] == nil {
		return ""
	}
	return fmt.Sprint(row[i])
}

func TokenWhyEvidence(rows [][]any, q string, colIdx map[string]int) []WhyEvidence {
	tokens := tokenizeQuery(q)
	if len(tokens) == 0 || len(rows) == 0 {
		return nil
	}
	normTokens := make([]string, len(tokens))
	for i, tok := range tokens {
		normTokens[i] = normalizeTokenText(tok)
	}
	topicIdx, summaryIdx := -1, -1
	if v, ok := colIdx["topic"]; ok {
		topicIdx = v
	}
	if v, ok := colIdx["summary"]; ok {
		summaryIdx = v
	}
	out := make([]WhyEvidence, len(rows))
	for i, row := range rows {
		t := normalizeTokenText(cellString(row, topicIdx))
		s := normalizeTokenText(cellString(row, summaryIdx))
		parts, cover, total := tokenRowBreakdown(normTokens, t, s)
		listing := "none"
		if len(parts) > 0 {
			listing = strings.Join(parts, "+") + "+cover×" + strconv.Itoa(cover)
		}
		out[i] = WhyEvidence{ID: whyRowID(row), Tokens: listing, TokenScore: total}
	}
	return out
}

func RRFWhyEvidence(rows [][]any, tokens []string) []WhyEvidence {
	if len(rows) == 0 {
		return nil
	}
	values := rrfSignalValues(rows, tokens)
	ranks := rrfRanks(values)
	scores := rrfFuse(values)
	out := make([]WhyEvidence, len(rows))
	for i, row := range rows {
		sigs := make([]RRFSignalEvidence, len(rrfSignalNames))
		for s, name := range rrfSignalNames {
			contrib := 0.0
			if ranks[i][s] > 0 {
				contrib = 1.0 / (rrfK + float64(ranks[i][s]))
			}
			sigs[s] = RRFSignalEvidence{Name: name, Value: values[i][s], Rank: ranks[i][s], Contrib: contrib}
		}
		out[i] = WhyEvidence{ID: whyRowID(row), RRF: sigs, RRFScore: scores[i]}
	}
	return out
}

type WhyRowLine struct {
	Why WhyEvidence `json:"why"`
}

type WhyZero struct {
	ZeroReason string  `json:"zero_reason"`
	NearScore  float64 `json:"near_score"`
}

type WhyZeroLine struct {
	Why WhyZero `json:"why"`
}

func FormatWhyRows(ev []WhyEvidence, jsonMode bool) string {
	if len(ev) > WhyMaxRows {
		ev = ev[:WhyMaxRows]
	}
	var lines []string
	for _, e := range ev {
		prov := FormatProvenanceSuffix(e.Source, e.Confidence)
		if jsonMode {
			b, err := json.Marshal(WhyRowLine{Why: e})
			if err != nil {
				continue
			}
			lines = append(lines, string(b))
			continue
		}
		if len(e.RRF) > 0 {
			sigs := make([]string, 0, len(e.RRF))
			for _, s := range e.RRF {
				sigs = append(sigs, fmt.Sprintf("%s=%d/r%d/%s",
					s.Name, s.Value, s.Rank, strconv.FormatFloat(s.Contrib, 'g', -1, 64)))
			}
			lines = append(lines, strings.Join([]string{
				"why",
				"id=" + noPipe(e.ID),
				strings.Join(sigs, ";"),
				"score=" + strconv.FormatFloat(e.RRFScore, 'g', -1, 64),
			}, "|")+prov)
			continue
		}
		lines = append(lines, strings.Join([]string{
			"why",
			"id=" + noPipe(e.ID),
			noPipe(e.Tokens),
			"score=" + strconv.Itoa(e.TokenScore),
		}, "|")+prov)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func FormatWhyZero(zeroReason string, nearScore float64, jsonMode bool) string {
	if jsonMode {
		b, _ := json.Marshal(WhyZeroLine{Why: WhyZero{ZeroReason: zeroReason, NearScore: nearScore}})
		return string(b) + "\n"
	}
	return "why|zero_reason=" + zeroReason +
		"|near_score=" + strconv.FormatFloat(nearScore, 'g', -1, 64) + "\n"
}

func FormatRows(rows [][]any, fields string, jsonMode bool) string {
	if fields == "" {
		fields = "id,topic,summary"
	}
	names := strings.Split(fields, ",")
	var lines []string
	for _, row := range rows {
		vals := make([]string, 0, len(names))
		for i, name := range names {
			var raw any
			if idx := readColumnIndexOf(name); idx >= 0 {
				if idx < len(row) {
					raw = row[idx]
				}
			} else if i < len(row) {
				raw = row[i]
			}
			v := ""
			if raw != nil {
				v = fmt.Sprint(raw)
			}
			if name == "summary" {
				v = truncateRunes(v, summaryMax)
			}
			vals = append(vals, v)
		}
		if jsonMode {
			m := make(map[string]string, len(names))
			for i, name := range names {
				m[name] = vals[i]
			}

			b, err := json.Marshal(m)
			if err != nil {
				continue
			}
			lines = append(lines, string(b))
		} else {
			lines = append(lines, strings.Join(vals, "|"))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
