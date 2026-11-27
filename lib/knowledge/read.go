package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var ParseAsOfSpecFn func(ctx context.Context, st wire.Store, spec string) (string, error)

var ErrInvalidAsOf error

type readBumper interface {
	BumpReads(ctx context.Context, ids []int) error
}

type ReadEventStore interface {
	RecordReadEvent(ctx context.Context, ev db.ReadEvent) error
}

type BytesRecorder interface {
	RecordBytes(ctx context.Context, kind string, n int) error
}

type KnowledgeReadsRecorder interface {
	RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error
}

type RecallBumper interface {
	BumpRecallScores(ctx context.Context, ids []int) error
}

func recallCountsFrom(ctx context.Context, st wire.Store) map[int]int {
	rrows, err := wire.QueryOnce(ctx, st, RecallAggregateSQL, nil)
	if err != nil || len(rrows) == 0 {
		return nil
	}
	m := make(map[int]int, len(rrows))
	for _, r := range rrows {
		id := recallAnyInt(r[0])
		if id == 0 {
			continue
		}
		m[id] = recallAnyInt(r[1])
	}
	return m
}

func recallAnyInt(v any) int {
	switch n := v.(type) {
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

func recordKnowledgeReads(ctx context.Context, st wire.Store, ids []int, session, kind string) {
	if kr, ok := st.(KnowledgeReadsRecorder); ok {
		_ = kr.RecordKnowledgeReads(ctx, ids, session, kind)
	}
	if kind == "read" {
		if rb, ok := st.(RecallBumper); ok {
			_ = rb.BumpRecallScores(ctx, ids)
		}
	}
}

type TelemetryCommitter interface {
	CommitTelemetry(ctx context.Context, cmd string) error
}

func commitTelemetry(ctx context.Context, st wire.Store, cmd string) {
	if tc, ok := st.(TelemetryCommitter); ok {
		_ = tc.CommitTelemetry(ctx, cmd)
	}
}

const defaultZeroOwnedFloor = 1

const zeroOwnedFloorKey = "zero_owned_floor"

func zeroOwnedFloor(ctx context.Context, st wire.Store) int {
	if v, ok := wire.GetMetaKV(ctx, st, zeroOwnedFloorKey); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return defaultZeroOwnedFloor
}

func activeKnowledgeCount(ctx context.Context, st wire.Store) int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM knowledge WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	if n, ok := render.CellToInt(rows[0][0]); ok {
		return n
	}
	return 0
}

func ZeroReason(zero bool, active, floor int) string {
	if !zero {
		return ""
	}
	if active < floor {
		return "unowned"
	}
	return "owned_miss"
}

const gapQueryMaxRunes = 80

func NormalizeGapQuery(query string) string {
	s := norm.NFKC.String(query)
	s = render.TruncateRunes(s, gapQueryMaxRunes)
	s = render.Text(s)
	s = strings.ToLower(s)
	return strings.Join(strings.Fields(s), " ")
}

func GapQueryHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func BumpGapQuery(ctx context.Context, st wire.Store, sanitizedQuery string) (count int64, ok bool) {
	day := time.Now().UTC().Format("2006-01-02")
	hash := GapQueryHash(NormalizeGapQuery(sanitizedQuery))
	if err := db.BumpDailyFlatKey(ctx, st, "gapq:"+hash, day, 1); err != nil {
		return 0, false
	}

	val, _, err := wire.GetMetaKVErr(ctx, st, "daily:gapq:"+hash+":"+day)
	if err != nil || val == "" {
		return 0, false
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
	if perr != nil {
		return 0, false
	}
	return n, true
}

func recordBytes(ctx context.Context, st wire.Store, kind string, n int) {
	if br, ok := st.(BytesRecorder); ok {
		_ = br.RecordBytes(ctx, kind, n)
	}
}

func idsCSV(ids []int) string {
	if len(ids) > 10 {
		ids = ids[:10]
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	return strings.Join(parts, ",")
}

func FirstExactRank(rows [][]any, q string) int {

	lq := strings.ToLower(q)
	if lq == "" {
		return 0
	}
	for i, r := range rows {
		if len(r) < 2 {
			continue
		}
		if strings.ToLower(fmt.Sprint(r[1])) == lq {
			return i + 1
		}
	}
	return 0
}

func ParseIDsCSV(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func RowIDs(rows [][]any) []int {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if id, ok := render.CellToInt(row[0]); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func InstrumentReads(ctx context.Context, st wire.Store, kind string, ids []int, zero, near bool) {
	if rb, ok := st.(readBumper); ok {
		_ = rb.BumpReads(ctx, ids)
	}
	if kind == "read" {
		return
	}
	RecordUsage(ctx, st, kind, "")
}

func RunRead(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--json"},
			{Name: "--tag", Value: true},
			{Name: "--entity", Value: true},
			{Name: "--domain", Value: true},
			{Name: "--limit", Value: true},
			{Name: "--status", Value: true},
			{Name: "--as-of", Value: true},
			{Name: "--fields", Value: true},

			{Name: "--min-confidence", Value: true},
			{Name: "--origin", Value: true},
			{Name: "--claim", Value: true},

			{Name: "--session", Value: true},
			{Name: "--source", Value: true},

			{Name: "--related", Value: true},

			{Name: "--why"},
		},
		Usage: i18n.T("usage.read"),

		DashDash:        true,
		AllowPositional: true,
	})
	if perr != nil {

		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	f := query.Filters{
		Tag:    res.Value("--tag"),
		Entity: res.Value("--entity"),
		Domain: res.Value("--domain"),
	}
	jsonMode := res.Given("--json")
	fields, asOfSpec, statusSpec := res.Value("--fields"), res.Value("--as-of"), res.Value("--status")
	session, sourceSpec := res.Value("--session"), res.Value("--source")
	asOfGiven, statusGiven := res.Given("--as-of"), res.Given("--status")
	sessionGiven, sourceGiven := res.Given("--session"), res.Given("--source")
	fieldsGiven := res.Given("--fields")
	if res.Given("--limit") {
		n, err := strconv.Atoi(res.Value("--limit"))
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "limit", Expect: "integer"}))
			return 3
		}
		f.Limit = n
	}

	if res.Given("--min-confidence") {
		v, err := strconv.ParseFloat(res.Value("--min-confidence"), 64)

		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "min-confidence", Expect: "0..1 float"}))
			return 3
		}
		f.MinConfidence = &v
	}

	if res.Given("--origin") {
		switch res.Value("--origin") {
		case "agent", "human":
			f.Origin = res.Value("--origin")
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "origin", Expect: "agent|human"}))
			return 3
		}
	}

	if res.Given("--claim") {
		switch res.Value("--claim") {
		case "decision", "procedure", "lesson", "event":
			f.Claim = res.Value("--claim")
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "claim",
				Expect: "decision|procedure|lesson|event"}))
			return 3
		}
	}
	q := strings.Join(res.Positional(), " ")

	relatedID := 0
	if res.Given("--related") {
		n, err := strconv.Atoi(res.Value("--related"))
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "related", Expect: "integer"}))
			return 3
		}
		if n > math.MaxInt32 {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "related",
				Expect: fmt.Sprintf("knowledge id over the int4 ceiling %d", math.MaxInt32)}))
			return 3
		}
		relatedID = n
	}

	for _, g := range []struct {
		given bool
		val   string
		field string
	}{
		{res.Given("--tag"), f.Tag, "tag"},
		{res.Given("--entity"), f.Entity, "entity"},
		{res.Given("--domain"), f.Domain, "domain"},
		{fieldsGiven, fields, "fields"},
	} {
		if g.given && g.val == "" {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: g.field, Expect: "non-empty"}))
			return 3
		}
	}

	if !sessionGiven {
		if label, ok := wire.ReadSessionLabel(); ok {
			session = label
		}
	}

	if err := validateSessionLabel(sessionGiven, session); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}

	source := sourceSpec
	if sourceGiven {
		switch source {
		case "hook", "cli", "mcp":
		case "":
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "source", Expect: "non-empty"}))
			return 3
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "source", Expect: "hook|cli|mcp"}))
			return 3
		}
	} else if session != "" {
		source = "hook"
	} else {
		source = "cli"
	}

	if fieldsGiven {
		for _, name := range strings.Split(fields, ",") {
			if name = strings.TrimSpace(name); name == "" || !query.ReadFieldKnown(name) {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
					Field:  "fields",
					Expect: "known columns: " + query.ReadColumnsHint(),
				}))
				return 3
			}
		}
	}
	asOfHash, asOfDate := "", ""

	if asOfGiven && asOfSpec == "" {
		fmt.Fprintln(stdout, asOfErrLine("invalid_as_of",
			fmt.Errorf("empty as-of spec: %w", ErrInvalidAsOf).Error()))
		return 3
	}

	if statusGiven || statusSpec != "" {
		switch statusSpec {
		case "pending", "active", "invalidated":
			if asOfSpec == "" {
				f.Status = statusSpec
			}
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
				Field: "status", Expect: "pending|active|invalidated"}))
			return 3
		}
	}

	if res.Given("--related") {
		if erows, eerr := wire.QueryOnce(ctx, st,
			`SELECT id FROM knowledge WHERE id = $1`, []any{relatedID}); eerr == nil && len(erows) == 0 {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
				Field:  "related",
				Expect: fmt.Sprintf("existing knowledge id (id=%d not found)", relatedID)}))
			return 3
		}
	}
	var sql string
	var qargs []any
	if f.Status != "" {
		sql, qargs = query.BuildReadStatus(q, f)
	} else {
		sql, qargs = query.BuildRead(q, f)
	}
	var rows [][]any
	if asOfSpec != "" {
		hash, err := ParseAsOfSpecFn(ctx, st, asOfSpec)
		if err != nil {
			if errors.Is(err, ErrInvalidAsOf) {
				fmt.Fprintln(stdout, asOfErrLine("invalid_as_of", err.Error()))
				return 3
			}
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}

		aq := splitReadAsOf(sql, qargs)
		aq.Hash = hash
		asOfHash = hash

		if les, lerr := st.LogEntries(ctx, wire.LogQuery{Hash: hash}); lerr == nil && len(les) == 1 {
			asOfDate = FmtAsOfDate(les[0].Date)
		}
		rows, err = st.QueryAsOf(ctx, aq)
		if err != nil {

			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	} else {
		var err error
		rows, err = wire.QueryOnce(ctx, st, sql, qargs)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}

	fallbackTried := false

	var th store.Thresholds
	thOK := false
	if ReadSuggestEligible(len(rows), asOfSpec, f.Status) {

		if loaded, thErr := wire.LoadThresholdsFn(ctx, st); thErr == nil {
			th, thOK = loaded, true
			query.SetRRFK(th.RrfK)
		}
		if fsql, fargs, ok := query.BuildReadFallback(q, f); ok {
			BumpReadFallbackToMeta(ctx, st)
			fallbackTried = true
			if frows, ferr := wire.QueryOnce(ctx, st, fsql, fargs); ferr == nil && len(frows) > 0 {
				rows = frows
				query.SetRecallCounts(recallCountsFrom(ctx, st))
				query.ReorderByFallbackCount(rows, query.TokenizeQuery(q))
			}
		}
	}

	if !fallbackTried {
		rows = query.ReorderByTokenScore(rows, q, query.ReadColumnIndexMap())
	}

	whyGiven := res.Given("--why")
	var whyRows []query.WhyEvidence
	if whyGiven {
		if fallbackTried {
			whyRows = query.RRFWhyEvidence(rows, query.TokenizeQuery(q))
		} else {
			whyRows = query.TokenWhyEvidence(rows, q, query.ReadColumnIndexMap())
		}
		if len(whyRows) > query.WhyMaxRows {
			whyRows = whyRows[:query.WhyMaxRows]
		}
	}

	var suggestions []query.Suggestion

	scanMax := 0.0
	if ReadSuggestEligible(len(rows), asOfSpec, f.Status) {

		if thOK {
			query.SetSuggestThresholds(th.SuggestMinScore, th.SuggestScanCap)
			query.SetRRFK(th.RrfK)
		}
		ssql, sargs := query.BuildSuggestScan()
		if srows, serr := wire.QueryOnce(ctx, st, ssql, sargs); serr == nil {
			suggestions, scanMax = query.SuggestSimilarWithScanMax(suggestQuery(q), srows, 3)
		}
	}

	var zeroReasonVal string
	zeroReasonDone := false
	zeroReasonOf := func() string {
		if !zeroReasonDone {
			zeroReasonDone = true
			if len(rows) == 0 {
				zeroReasonVal = ZeroReason(true, activeKnowledgeCount(ctx, st), zeroOwnedFloor(ctx, st))
			}
		}
		return zeroReasonVal
	}

	rows = render.Cells(rows)

	var vaultRows [][]any
	if q != "" {
		vsql, vargs := query.BuildVaultRead(q)
		if vrows, verr := wire.QueryOnce(ctx, st, vsql, vargs); verr == nil {
			vaultRows = render.Cells(vrows)

			if len(vaultRows) > query.VaultMaxRows {
				vaultRows = vaultRows[:query.VaultMaxRows]
			}
		}
	}

	var relatedRows [][]any
	if res.Given("--related") {
		rsql, rargs := query.BuildRelatedRead(relatedID)
		if rrows, rerr := wire.QueryOnce(ctx, st, rsql, rargs); rerr == nil {
			relatedRows = render.Cells(rrows)

			if len(relatedRows) > query.RelatedMaxRows {
				relatedRows = relatedRows[:query.RelatedMaxRows]
			}
		}
	}

	out := query.FormatRows(rows, fields, jsonMode)

	vaultOut := query.FormatVaultRows(vaultRows, jsonMode)

	relatedOut := query.FormatRelatedRows(relatedRows, jsonMode)
	fmt.Fprint(stdout, out+vaultOut+relatedOut)

	byteLen := len(out) + len(vaultOut) + len(relatedOut)

	if len(rows) == 0 && jsonMode {
		b, _ := json.Marshal(ZeroHitLine{Hits: 0})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1
	}

	if jsonMode && len(vaultRows) > 0 {
		b, _ := json.Marshal(VaultHitsLine{VaultHits: len(vaultRows)})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1
	}

	if jsonMode && len(relatedRows) > 0 {
		b, _ := json.Marshal(RelatedHitsLine{RelatedHits: len(relatedRows)})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1
	}

	whyOut := ""
	if whyGiven {
		if len(rows) == 0 {
			whyOut = query.FormatWhyZero(zeroReasonOf(), scanMax, jsonMode)
		} else {
			whyOut = query.FormatWhyRows(whyRows, jsonMode)
		}
		if whyOut != "" {
			fmt.Fprint(stdout, whyOut)
			byteLen += len(whyOut)
		}
	}

	if jsonMode && query.ClampLimit(f.Limit) < f.Limit {
		b, _ := json.Marshal(LimitClampLine{
			LimitRequested: f.Limit,
			LimitApplied:   query.ClampLimit(f.Limit),
		})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1
	}
	if asOfHash != "" {
		b, _ := json.Marshal(AsOfLine{AsOf: AsOfInfo{Hash: asOfHash, CommitDate: asOfDate}})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1
	}

	recordBytes(ctx, st, "read", byteLen)

	rowIDs := RowIDs(rows)
	InstrumentReads(ctx, st, "read", rowIDs, len(rows) == 0, len(suggestions) > 0)

	recordKnowledgeReads(ctx, st, rowIDs, session, "read")

	if res, ok := st.(ReadEventStore); ok {

		rank := 0
		if !fallbackTried && asOfSpec == "" && f.Status == "" {
			rank = FirstExactRank(rows, q)
		}

		zeroReason := zeroReasonOf()
		_ = res.RecordReadEvent(ctx, db.ReadEvent{
			Hits:       len(rows),
			Zero:       len(rows) == 0,
			Near:       len(suggestions) > 0,
			Fallback:   fallbackTried,
			Session:    session,
			IDs:        idsCSV(rowIDs),
			FirstRank:  rank,
			Bytes:      byteLen,
			ZeroReason: zeroReason,
			Source:     source,
			NearScore:  scanMax,
		})

		if zeroReason == "owned_miss" && asOfSpec == "" {
			if n, ok := BumpGapQuery(ctx, st, q); ok {
				maybeAccrueGapCandidate(ctx, st, q, n)
			}
		}
	}

	if _, cerr := st.Commit(ctx, "read: recall bump (read audit attribution)"); cerr != nil {
		fmt.Fprintf(os.Stderr, "[flint] read audit commit: %v\n", cerr)
	}

	commitTelemetry(ctx, st, "read")

	if PersistScanStatsFn != nil {
		PersistScanStatsFn(ctx, st)
	}
	return 0
}

const sessionLabelMaxRunes = 200

func validateSessionLabel(given bool, val string) error {
	if !given {
		return nil
	}
	if val == "" {
		return &store.SchemaError{Field: "session", Expect: "non-empty"}
	}
	if strings.HasPrefix(val, "-") {
		return &store.SchemaError{Field: "session", Expect: "label, not a flag"}
	}
	if strings.ContainsFunc(val, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return &store.SchemaError{Field: "session", Expect: "single-line label without control characters"}
	}
	if utf8.RuneCountInString(val) > sessionLabelMaxRunes {
		return &store.SchemaError{Field: "session", Expect: fmt.Sprintf("label up to %d runes", sessionLabelMaxRunes)}
	}
	return nil
}

func ValidateSessionLabel(given bool, val string) error {
	return validateSessionLabel(given, val)
}

func ReadSuggestEligible(rows int, asOfSpec, status string) bool {
	return rows == 0 && asOfSpec == "" && status == ""
}

const suggestQueryMaxRunes = 4096

func suggestQuery(q string) string {
	return render.TruncateRunes(q, suggestQueryMaxRunes)
}

func BumpReadFallbackToMeta(ctx context.Context, st wire.Store) {
	_ = wire.BumpMetaKV(ctx, st, query.ReadFallbackCountKey)
}

type AsOfInfo struct {
	Hash       string `json:"hash"`
	CommitDate string `json:"commit_date"`
}

type AsOfLine struct {
	AsOf AsOfInfo `json:"as_of"`
}

type ZeroHitLine struct {
	Hits int `json:"hits"`
}

type LimitClampLine struct {
	LimitRequested int `json:"limit_requested"`
	LimitApplied   int `json:"limit_applied"`
}

type VaultHitsLine struct {
	VaultHits int `json:"vault_hits"`
}

type RelatedHitsLine struct {
	RelatedHits int `json:"related_hits"`
}

func asOfErrLine(code, msg string) string {
	b, _ := json.Marshal(map[string]string{"error": code, "message": msg})
	return string(b)
}

func FmtAsOfDate(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}

var asOfHashRe = regexp.MustCompile(`^[0-9a-z]{6,64}$`)

func ValidAsOfHash(hash string) bool {
	return asOfHashRe.MatchString(hash)
}

func splitReadAsOf(sql string, args []any) wire.AsOfQuery {
	from := " FROM active_knowledge"
	table := "active_knowledge"
	if i := strings.Index(sql, " FROM recall_knowledge"); i >= 0 {
		from, table = " FROM recall_knowledge", "recall_knowledge"
	} else if i := strings.Index(sql, " FROM knowledge "); i >= 0 {
		from, table = " FROM knowledge", "knowledge"
	}
	var q wire.AsOfQuery
	i := strings.Index(sql, from)
	q.Table = table
	head := strings.TrimPrefix(sql[:i], "SELECT ")
	tail := sql[i+len(from):]

	if j := strings.LastIndex(tail, " LIMIT $"); j >= 0 {
		tail = tail[:j]
	}
	q.Columns = strings.Split(head, ", ")
	q.Where = strings.TrimSpace(tail)
	q.Args = args
	if n, ok := args[len(args)-1].(int); ok {
		q.Limit, q.Args = n, args[:len(args)-1]
	}
	return q
}
