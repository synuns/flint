// Package query — the flint read/hot query assembly. Reads use the active_knowledge view by default;
// only with --status does it query the base table knowledge directly.
package query

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Filters — the flint read filters (--tag --entity --domain --limit --status
// --min-confidence --origin).
type Filters struct {
	Tag    string
	Entity string
	Domain string
	Limit  int
	// Status — --status pending|active|invalidated. When specified,
	// BuildReadStatus queries the base table instead of BuildRead. BuildRead's
	// unspecified path ignores this value (the view path fully unchanged — ruling).
	Status string
	// MinConfidence — --min-confidence N (a 0..1 float). nil = the filter is
	// absent (no floor — the default). Assembled as `confidence >= $n` by
	// appendReadFilters; no thresholds registry involvement (TUNING §2).
	MinConfidence *float64
	// Origin — --origin agent|human. "" = the filter is absent.
	Origin string
}

const (
	defaultLimit = 10
	maxLimit     = 50
	summaryMax   = 80

	// ReadFallbackCountKey — the meta metering key for zero-hit fallback attempts.
	ReadFallbackCountKey = "read_fallback_calls"
	// maxQueryTokens — the fallback OR-scan cost guard (ruling): excess tokens are dropped.
	maxQueryTokens = 8
)

// ClampLimit — limit defaults to 10, capped at 50 (the token budget is baked in).
func ClampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// readColumns — the columns BuildRead SELECTs (fixed order). The basis of
// FormatRows's field-name -> position mapping.
var readColumns = []string{
	"id", "topic", "summary", "tags", "entity", "domain", "confidence", "status",
	"read_count",
}

// tokenizeQuery — splits the free-text q into search tokens (for the fallback).
// After strings.Fields splits, leading/trailing punctuation (unicode.IsPunct) of each
// token is removed and empty tokens are dropped. The cap is 8 — excess tokens are silently dropped (not an error).
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

// TokenizeQuery — the export of tokenizeQuery (for the fallback-rerank call sites and lib/cli).
func TokenizeQuery(q string) []string { return tokenizeQuery(q) }

// trimPunct — strips punctuation from both ends of a token (inner punctuation kept — 'a-b' etc.).
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

// fallbackMatchCount — the number of tokens contained in the topic or summary (the rerank
// score). Case-insensitive lower substring matching. NFKC normalization is applied to both
// sides of the comparison (blocking misses from NFC/NFD notation differences. The originals and SQL are unchanged).
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

// likeEscape — escapes \, %, and _ as literals in a LIKE pattern.
// Single quotes are passed as placeholder parameters, so they need no separate escaping.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike — escapes the metacharacters (\, %, _) of a literal value inlined into a
// LIKE pattern. Other call sites such as audit --session reuse the same rule as the read
// assembler's internal one (backslash as the default ESCAPE — matching the dialect SQL contract).
func EscapeLike(s string) string { return likeEscaper.Replace(s) }

// RankOrder — the search-ranking sort clause: exact topic match first -> confidence ->
// newest ingested.
// Only on ties does recent reference win — a trailing last_read_at DESC NULLS LAST is
// added (derived at query time; stored values unchanged). With NULLS LAST, unreferenced
// items rank last among ties. Adoption rerank — after last_read_at, a final tiebreak of
// read_count DESC is added (more-read items first). The upper ranking elements are unchanged.
// q is a parameter, not an inline literal — the caller appends it to args and
// receives its index (matching the LIKE clause contract of the same query,
// removing the standard_conforming_strings dependency).
func RankOrder(placeholder int) string {
	return fmt.Sprintf("ORDER BY (lower(topic)=lower($%d)) DESC, confidence DESC, ingested_at DESC, last_read_at DESC NULLS LAST, read_count DESC",
		placeholder)
}

// appendReadFilters — the shared body assembling the q/tag/entity/domain filters'
// where-clauses and placeholder arguments: both BuildRead and BuildReadStatus call it, so
// the two assemblers' filter interpretation is always identical (preventing copy-paste
// divergence). The append order = the where-clause order = the placeholder numbers — appended after the caller's preceding conditions (status, etc.).
func appendReadFilters(where []string, args []any, q string, f Filters) ([]string, []any) {
	if q != "" {
		args = append(args, "%"+likeEscaper.Replace(q)+"%")
		where = append(where, fmt.Sprintf("lower(topic || ' ' || summary) LIKE lower($%d)", len(args)))
	}
	if f.Tag != "" {
		args = append(args, f.Tag)
		// the tags storage form ':a:b:' — exact ':tag:' matching to prevent substring false positives.
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

// BuildRead — assembles the active_knowledge view query SQL and placeholder arguments.
// The free-text q matches via lower(topic||' '||summary) LIKE lower('%q%') (an ILIKE substitute).
func BuildRead(q string, f Filters) (string, []any) {
	var where []string
	var args []any

	where, args = appendReadFilters(where, args, q, f)

	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM active_knowledge"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, q) // the ranking-consistency value, parameterized
	sql += " " + RankOrder(len(args))
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args
}

// BuildReadStatus — assembles the query SQL for the --status path. Since the view
// exposes only rows with invalid_at IS NULL, viewing pending or invalidated rows requires
// filtering the base table knowledge directly. The status condition goes first as a
// parameter; the q/tag/entity/domain filters use the shared appendReadFilters assembly;
// RankOrder, LIMIT, and likeEscape inherit the BuildRead assembly. The columns and order
// stay readColumns as-is (the view and the base table share the same source table).
// status='active' filters by the view's exposure rule (invalid_at IS NULL) equivalently,
// preventing a definitional split where rows absent from the view appear only under
// --status active. pending and invalidated do not get this condition — for invalidated,
// having invalid_at set is the essence of the state (attaching it would yield zero hits);
// pending is a pre-promotion state with a different invalid_at convention (ruling).
func BuildReadStatus(q string, f Filters) (string, []any) {
	var where []string
	var args []any

	args = append(args, f.Status)
	where = append(where, fmt.Sprintf("status = $%d", len(args)))
	if f.Status == "active" {
		where = append(where, "invalid_at IS NULL")
	}
	where, args = appendReadFilters(where, args, q, f)

	args = append(args, q) // the ranking-consistency value, parameterized
	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM knowledge WHERE " +
		strings.Join(where, " AND ") + " " + RankOrder(len(args))
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args
}

// BuildReadFallback — assembles the zero-hit fallback query SQL. Per-token
// OR LIKE + the existing filters (tag, entity, domain), the active_knowledge view,
// likeEscape, RankOrder, and LIMIT are inherited. Fewer than 2 tokens is identical to
// phrase matching, so ok=false (fallback skipped — ruling).
func BuildReadFallback(q string, f Filters) (string, []any, bool) {
	tokens := tokenizeQuery(q)
	if len(tokens) < 2 {
		return "", nil, false
	}
	var conds []string
	var args []any
	for _, tok := range tokens {
		args = append(args, "%"+likeEscaper.Replace(tok)+"%")
		// the fallback's matched surface includes tags — knowledge classified only by tags is
		// also recovered by the word fallback (substring matching against the ':a:b:' tags
		// storage form). The phrase query's matched surface is unchanged (tags belong to the --tag filter domain).
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
	// the new filters (min-confidence·origin) inherit the same floor on the
	// retry — otherwise the fallback would resurface the rows the phrase
	// query just excluded (the tag/entity/domain convention extended).
	if f.MinConfidence != nil {
		args = append(args, *f.MinConfidence)
		where = append(where, fmt.Sprintf("confidence >= $%d", len(args)))
	}
	if f.Origin != "" {
		args = append(args, f.Origin)
		where = append(where, fmt.Sprintf("origin = $%d", len(args)))
	}
	args = append(args, q) // the ranking-consistency value, parameterized
	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM active_knowledge WHERE " +
		strings.Join(where, " AND ") + " " + RankOrder(len(args))
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args, true
}

// rrfK — the RRF rank-softening coefficient (the original paper's standard 60). The
// runtime override is SetRRFK (the thresholds rrf_k key — a single tuning source).
var rrfK = 60.0

// SetRRFK — the thresholds registry -> query injection (inheriting SetSuggestThresholds).
func SetRRFK(k float64) {
	if k > 0 {
		rrfK = k
	}
}

// ReorderByFallbackCount — the app-side rerank of fallback results. RRF fusion —
// match count, read_count, jamo similarity, and topic match (recovering the phrase path's
// RankOrder exact-topic-first priority that would otherwise be lost in fallback fusion)
// — 4 signals each ranked descending, summed as 1/(k+rank).
// Multiple signal contributions can beat a single-signal advantage (a contract different
// from match-count-first with sequential tiebreaks).
// Ties keep the existing query order (the SQL RankOrder).
func ReorderByFallbackCount(rows [][]any, tokens []string) {
	if len(rows) < 2 {
		return
	}
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
		// normalization applied up front — reapplying it inside the scorer is idempotent (the result is identical).
		t := nfkc(strings.ToLower(topic))
		sm := nfkc(strings.ToLower(summary))
		values[i] = []int{
			fallbackMatchCount(tokens, t, sm),
			readCountOf(row),
			jamoOverlap(tokenJamo, t, sm),
			topicMatchCount(tokens, t), // the topic-match signal
		}
	}
	scores := rrfFuse(values)
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if scores[idx[a]] != scores[idx[b]] {
			return scores[idx[a]] > scores[idx[b]]
		}
		return idx[a] < idx[b] // ties keep the existing order (the SQL RankOrder)
	})
	out := make([][]any, len(rows))
	for pos, src := range idx {
		out[pos] = rows[src]
	}
	copy(rows, out)
}

// ── v1.16.0 T1 — the phrase-hit token rerank. ──

// The per-token tier scores of ReorderByTokenScore: a topic prefix is the
// strongest signal (carrying RankOrder's exact-topic-first convention into
// the window), then a summary prefix, then containment in either field.
const (
	tokenTopicPrefixScore    = 4
	tokenSummaryPrefixScore  = 3
	tokenTopicContainScore   = 2
	tokenSummaryContainScore = 1
)

// normalizeTokenText — the token scorer's shared normalization: NFKC
// (composed/decomposed spellings converge) + lower + whitespace fold (runs
// collapse to one space, edges trimmed). Applied to both the query tokens
// and the row fields — the originals and the SQL are unchanged.
func normalizeTokenText(s string) string {
	return strings.Join(strings.Fields(nfkc(strings.ToLower(s))), " ")
}

// cellString — a row cell as a string by column index (an out-of-range
// index or a non-string cell is ""). The robustness helper of the token
// scorer (short rows never panic).
func cellString(row []any, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	if s, ok := row[idx].(string); ok {
		return s
	}
	return ""
}

// tokenTierScore — one token's tier score against a row's normalized
// topic/summary (topic prefix > summary prefix > topic contains > summary
// contains; no match 0).
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

// ReorderByTokenScore — the app-side rerank of phrase-hit read rows
// (v1.16.0 T1): per-token tier scores are summed per row — each matched
// token adds its tier plus a +1 cover bonus, so covering more tokens beats
// a single lower-tier match. Both sides are normalized (NFKC·lower·
// whitespace fold — normalizeTokenText). Ties keep the incoming (SQL
// RankOrder) order (sort.SliceStable). colIdx maps the topic/summary
// column names to row positions (ReadColumnIndexMap is the single source);
// absent keys or short rows score 0 without panic. The SQL LIMIT stays —
// this reorders within the returned window only. The caller gates on
// !fallbackTried: rows already reranked by ReorderByFallbackCount are not
// re-reordered.
func ReorderByTokenScore(rows [][]any, q string, colIdx map[string]int) [][]any {
	tokens := tokenizeQuery(q)
	if len(tokens) == 0 || len(rows) < 2 {
		return rows
	}
	normTokens := make([]string, len(tokens))
	for i, tok := range tokens {
		normTokens[i] = normalizeTokenText(tok)
	}
	// absent colIdx keys fall to -1 (a missing map key would read as 0 —
	// the id column — so the comma-ok form is load-bearing here).
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
		for _, tok := range normTokens {
			if tok == "" {
				continue
			}
			if tier := tokenTierScore(tok, t, s); tier > 0 {
				scores[i] += tier + 1 // the tier + the cover-token bonus
			}
		}
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

// ReadColumnIndexMap — the readColumns name→position map (the caller-side
// colIdx input of ReorderByTokenScore — the single source).
func ReadColumnIndexMap() map[string]int {
	m := make(map[string]int, len(readColumns))
	for i, c := range readColumns {
		m[c] = i
	}
	return m
}

// rrfFuse — per-row signal values -> the RRF score: summed as 1/(k+rank) from each
// signal's descending rank (1 = top; ties share a rank). Substituting the raw value for
// the rank flips the sign so the minimum wins (a first-draft defect — sealed off by a regression test).
func rrfFuse(values [][]int) []float64 {
	n := len(values)
	if n == 0 {
		return nil
	}
	scores := make([]float64, n)
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
			if values[row][sig] > 0 { // a 0 value means the signal contributes nothing
				scores[row] += 1.0 / (rrfK + float64(rank))
			}
		}
	}
	return scores
}

// topicMatchCount — the number of tokens appearing in the topic (the 4th RRF signal —
// reflecting in the fusion the RankOrder convention that a topic match is stronger than a
// summary match. Design note: it is a subset of matchCount, so it double-weights per token —
// an intentional weighting to recover topic-first RankOrder).
func topicMatchCount(tokens []string, t string) int {
	n := 0
	for _, tok := range tokens {
		if strings.Contains(t, nfkc(strings.ToLower(tok))) {
			n++
		}
	}
	return n
}

// jamoOverlap — jamo bigrams: an approximate similarity from the overlap of the jamo-
// decomposition bigrams of Korean tokens (absorbing typos and inflection). Tokens without
// Korean are 0 (no signal contribution — preventing double counting with the substring
// signal). tokenSets are precomputed outside the row loop.
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
				break // 1 point per token
			}
		}
	}
	return n
}

// jamoTokenSets — only Korean-containing tokens, as jamo-bigram sets.
func jamoTokenSets(tokens []string) []map[string]struct{} {
	var out []map[string]struct{}
	for _, tok := range tokens {
		// checked 'after' normalization — recovering the case where NFD-decomposed tokens (the
		// macOS paste path) silently missed the jamo signal.
		n := nfkc(strings.ToLower(tok))
		if containsHangul(n) {
			out = append(out, jamoBigramSet(n))
		}
	}
	return out
}

// containsHangul — whether a Korean syllable is present.
func containsHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}

// jamoBigramSet — the bigram set of the jamo-decomposed rune sequence (set-building
// delegates to bigramSet — a single implementation, review 4). Syllables decompose into cho/jung/jong.
func jamoBigramSet(s string) map[string]struct{} {
	const (
		syllableBase = 0xAC00
		leadBase     = 0x1100 // ㄱ
		vowelBase    = 0x1161 // ㅏ
		trailBase    = 0x11A7 // none + 1
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

// readColumnIndexOf — a column's position within readColumns (-1 = absent). For the
// rerank signal extraction inside the query package (the same convention as cli's columnIndexOf).
func readColumnIndexOf(name string) int {
	for i, c := range readColumns {
		if c == name {
			return i
		}
	}
	return -1
}

// readCountOf — the read_count value from a row (by readColumns index). Accepts pgx
// numerics; a missing column, non-numeric, or nil is 0 (treated as a tie — the sort is unchanged).
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

// columnIndexOf — fields name -> readColumns index. -1 when absent.
// ReadFieldKnown — whether a --fields name is a known column of the read result
// (preventing the distortion where a typo'd field silently filled another column's value
// via FormatRows's positional fallback — for up-front validation on the read path).
func ReadFieldKnown(name string) bool {
	for _, c := range readColumns {
		if c == name {
			return true
		}
	}
	return false
}

// ReadColumnsHint — the known-column list for validation-error guidance (a single source).
func ReadColumnsHint() string {
	return strings.Join(readColumns, ",")
}

// ── v1.21.0 T4 — the vault bridge read fusion. ──

// VaultMaxRows — the vault row cap of the read fusion (the plan contract
// constant). Vault rows are appended AFTER the knowledge rows and never
// reorder them; the cap bounds the additive token budget.
const VaultMaxRows = 5

// vaultTitleMax — the title truncation of the vault output lines: the same
// 200-rune cap the index applies (vault.titleMaxRunes) — the read output
// budget guard for the title column (v1.21.1 r93).
const vaultTitleMax = 200

// vaultColumns — the columns BuildVaultRead SELECTs (fixed order). The
// FormatVaultRows field-name -> position basis.
var vaultColumns = []string{"title", "summary", "tags", "path"}

// BuildVaultRead — assembles the separate vault_files query (the read
// fusion source, spec M2): the free-text q matches via lower(title ||
// ' ' || summary || ' ' || tags) LIKE lower('%q%') — the fallback's
// matched-surface convention. The knowledge Filters do NOT apply: the
// vault query is q-based only (Filters are the knowledge domain's shape
// and stay unchanged). ORDER BY path is deterministic; LIMIT is the
// VaultMaxRows constant (the caller caps app-side as well).
func BuildVaultRead(q string) (string, []any) {
	args := []any{"%" + likeEscaper.Replace(q) + "%"}
	sql := "SELECT " + strings.Join(vaultColumns, ", ") + " FROM vault_files" +
		fmt.Sprintf(" WHERE lower(title || ' ' || summary || ' ' || tags) LIKE lower($%d)", len(args)) +
		" ORDER BY path LIMIT " + strconv.Itoa(VaultMaxRows)
	return sql, args
}

// VaultLine — one vault row's --json shape: identified by path (no id) +
// the source:"vault" marker (the spec M2 row contract).
type VaultLine struct {
	Source  string `json:"source"`
	Path    string `json:"path"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// FormatVaultRows — the vault row output: one line per row, appended
// AFTER the knowledge rows (never interleaved — the knowledge-result
// invariance contract). Text mode "vault|path|title|summary" with '|'
// stripped from the cells (a cell-internal pipe would forge a fifth field
// and break the 4-field protocol — v1.21.1 r93; the SanitizeText
// replace-with-space convention); json mode a VaultLine per row. The
// summary is truncated to summaryMax and the title to vaultTitleMax — the
// read output token budget (the index-time caps alone do not bound the
// output surface).
func FormatVaultRows(rows [][]any, jsonMode bool) string {
	// vaultColumns is a fixed order: title=0, summary=1, tags=2, path=3.
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

// noPipe — replaces cell-internal '|' with a space (the text-mode
// 4-field protocol guard — the SanitizeText marker-replacement convention).
func noPipe(s string) string {
	return strings.ReplaceAll(s, "|", " ")
}

// truncateRunes — rune-based truncation (multibyte-safe, e.g. for Korean).
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// FormatRows — the query-result output format.
// Default: columns in fields order joined by '|'; the summary truncated to 80 runes.
// jsonMode: one line of JSON per row, containing only the selected fields keys.
// Field names are mapped against readColumns; names outside the mapping (hot_view's
// kind, ts, etc.) are interpreted by their position in the row.
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
				raw = row[i] // fields outside the mapping use the positional fallback
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
			// encoding/json serializes map keys sorted, so the output is stable.
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
