package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var kindColumns = map[string]map[string]bool{
	"knowledge": {"topic": true, "summary": true, "tags": true, "entity": true,
		"domain": true, "confidence": true, "origin": true, "status": true,
		"content_hash": true, "session_id": true, "valid_at": true,

		"conditions": true,

		"source_path": true},
	"session": {"ts": true, "agent": true, "summary": true},

	"project": {"name": true, "status": true, "updated": true, "note": true},
}

const rejectedTagPrefix = "rejected tag: "

var sourceRegistry = []string{"hook", "manual", "mcp", "correction", "gap"}

func sourceList() string { return strings.Join(sourceRegistry, "|") }

var ErrInvalidSource = fmt.Errorf("write: invalid --source (%s)", sourceList())

var validSources = func() map[string]bool {
	m := make(map[string]bool, len(sourceRegistry))
	for _, s := range sourceRegistry {
		m[s] = true
	}
	return m
}()

func correctionTodayCount(ctx context.Context, st wire.Store) int {
	key := "daily:correction_write:" + time.Now().UTC().Format("2006-01-02")
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	n := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &n); err != nil {
		return 0
	}
	return n
}

func applyCorrectionDefaults(p map[string]any, def float64) {
	if _, ok := p["confidence"]; !ok {
		p["confidence"] = def
	}
	switch tags := p["tags"].(type) {
	case []any:
		for _, t := range tags {
			if s, ok := t.(string); ok && s == "correction" {
				return
			}
		}
		p["tags"] = append(tags, "correction")
	case []string:
		for _, s := range tags {
			if s == "correction" {
				return
			}
		}
		p["tags"] = append(tags, "correction")
	default:
		p["tags"] = []any{"correction"}
	}
}

func applyCorrectionOrigin(p, n map[string]any) {
	if s, _ := p["origin"].(string); s == "" {
		n["origin"] = "correction"
	}
}

const gapCandidateConfidence = 0.4

const gapCandidateTag = "gap-candidate"

const gapCandidateTopicPrefix = "격차 후보: "

const gapCandidateTopicRunes = 60

const gapCandidateSummaryFmt = "회상 실패 %d회 — 지식 격차 후보"

const gapWriteDailyKind = "gap_write"

type pendingSummaryStore interface {
	UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error)
}

func gapCandidateHash(normalized string) string {
	topic := gapCandidateTopicPrefix + render.TruncateRunes(normalized, gapCandidateTopicRunes)
	n := store.Normalize("knowledge", map[string]any{
		"topic":      topic,
		"tags":       []any{gapCandidateTag},
		"confidence": gapCandidateConfidence,
	})
	return store.ContentHash(store.HashCanonical(n))
}

func gapTodayCount(ctx context.Context, st wire.Store) int {
	key := "daily:" + gapWriteDailyKind + ":" + time.Now().UTC().Format("2006-01-02")
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	n := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &n); err != nil {
		return 0
	}
	return n
}

func maybeAccrueGapCandidate(ctx context.Context, st wire.Store, sanitizedQuery string, count int64) {
	th := WriteThresholds(ctx, st)
	def := store.DefaultThresholds()
	threshold := th.GapQueryThreshold
	if threshold <= 0 {
		threshold = def.GapQueryThreshold
	}
	if count < int64(threshold) {
		return
	}
	normalized := NormalizeGapQuery(sanitizedQuery)
	topic := gapCandidateTopicPrefix + render.TruncateRunes(normalized, gapCandidateTopicRunes)
	summary := fmt.Sprintf(gapCandidateSummaryFmt, count)
	hash := gapCandidateHash(normalized)
	meta, exists, err := lookupHash(ctx, st, hash)
	if err != nil {
		return
	}
	if exists {

		if meta.State == "pending" {
			if ps, ok := st.(pendingSummaryStore); ok {
				if n, err := ps.UpdatePendingSummary(ctx, meta.ID, summary); err == nil && n > 0 {
					if _, cerr := st.Commit(ctx, "knowledge: gap candidate refresh"); cerr != nil {
						fmt.Fprintf(os.Stderr, "[flint] gap candidate refresh commit: %v\n", cerr)
					}
				}
			}
		}
		return
	}
	max := th.GapDailyMax
	if max <= 0 {
		max = def.GapDailyMax
	}
	if gapTodayCount(ctx, st) >= max {
		fmt.Fprintf(os.Stderr, "[flint] gap daily max reached (gap_daily_max=%d) — candidate skipped\n", max)
		return
	}

	rec := store.Normalize("knowledge", map[string]any{
		"topic":      topic,
		"summary":    summary,
		"tags":       []any{gapCandidateTag},
		"confidence": gapCandidateConfidence,
		"origin":     "gap",
	})
	rec["kind"] = "knowledge"
	rec["content_hash"] = hash
	rec["status"] = "pending"
	if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err != nil {
		return
	}
	_ = db.BumpDailyFlatKey(ctx, st, gapWriteDailyKind, time.Now().UTC().Format("2006-01-02"), 1)
	RecordUsage(ctx, st, "write", "gap")
	if _, err := st.Commit(ctx, "knowledge: gap candidate (recall failure)"); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] gap candidate commit: %v\n", err)
	}
}

func ParseSourceFlag(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		if args[i] != "--source" {
			continue
		}
		if i+1 >= len(args) {
			return "", ErrInvalidSource
		}
		src := args[i+1]
		if !validSources[src] {
			return "", ErrInvalidSource
		}
		return src, nil
	}
	return "manual", nil
}

type linkSpec struct {
	To       int
	Relation string
}

func parseLinkSpecs(p map[string]any) ([]linkSpec, *store.SchemaError) {
	raw, ok := p["relations"]
	if !ok || raw == nil {
		return nil, nil
	}
	delete(p, "relations")
	arr, ok := raw.([]any)
	if !ok {
		return nil, &store.SchemaError{Field: "relations", Expect: "array of {to,relation}"}
	}
	specs := make([]linkSpec, 0, len(arr))
	rels := make([]string, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, &store.SchemaError{Field: "relations", Expect: "array of {to,relation}"}
		}
		toF, ok := m["to"].(float64)
		if !ok || toF != math.Trunc(toF) || toF <= 0 {
			return nil, &store.SchemaError{Field: "relations", Expect: "to: positive integer row id"}
		}

		if toF >= 9223372036854775808.0 {
			return nil, &store.SchemaError{Field: "relations", Expect: "to: row id below 2^63"}
		}
		rel, ok := m["relation"].(string)
		if !ok {
			return nil, &store.SchemaError{Field: "relations", Expect: "relation: string"}
		}

		if len(rel) > 64 {
			return nil, &store.SchemaError{Field: "relations", Expect: "relation: at most 64 bytes"}
		}
		n := strings.ToLower(strings.TrimSpace(rel))
		specs = append(specs, linkSpec{To: int(toF), Relation: n})
		rels = append(rels, n)
	}
	for _, w := range store.CheckRelations(rels) {
		return nil, &store.SchemaError{Field: "relations", Expect: w}
	}
	return specs, nil
}

const MaxConditionsRunes = 500

func validateConditions(p map[string]any) *store.SchemaError {
	v, ok := p["conditions"]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return &store.SchemaError{Field: "conditions", Expect: "string"}
	}
	return ValidateConditionsString(s)
}

func ValidateConditionsString(s string) *store.SchemaError {
	if r := []rune(s); len(r) > MaxConditionsRunes {
		return &store.SchemaError{Field: "conditions",
			Expect: fmt.Sprintf("at most %d runes", MaxConditionsRunes)}
	}
	return nil
}

var knowledgeKindVocabulary = []string{"preference", "fact"}

var validKnowledgeKinds = map[string]bool{"preference": true, "fact": true}

func KnowledgeKindVocabulary() []string {
	return append([]string(nil), knowledgeKindVocabulary...)
}

const MaxSourcePathRunes = 1024

func parseKnowledgeKind(p map[string]any) (string, *store.SchemaError) {
	raw, ok := p["knowledge_kind"]
	if !ok || raw == nil {
		return "", nil
	}
	delete(p, "knowledge_kind")
	s, ok := raw.(string)
	if !ok {
		return "", &store.SchemaError{Field: "knowledge_kind", Expect: "string"}
	}
	n := strings.ToLower(strings.TrimSpace(s))
	if n == "" {
		return "", nil
	}
	if !validKnowledgeKinds[n] {
		return "", &store.SchemaError{Field: "knowledge_kind",
			Expect: strings.Join(knowledgeKindVocabulary, "|")}
	}
	return n, nil
}

func validateSourcePath(p map[string]any) *store.SchemaError {
	v, ok := p["source_path"]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return &store.SchemaError{Field: "source_path", Expect: "string"}
	}
	if r := []rune(s); len(r) > MaxSourcePathRunes {
		return &store.SchemaError{Field: "source_path",
			Expect: fmt.Sprintf("at most %d runes", MaxSourcePathRunes)}
	}
	return nil
}

func precheckLinkTargets(ctx context.Context, st wire.Store, specs []linkSpec) (*store.SchemaError, error) {
	seen := map[int]bool{}
	var tos []int
	for _, s := range specs {
		if !seen[s.To] {
			seen[s.To] = true
			tos = append(tos, s.To)
		}
	}
	if len(tos) == 0 {
		return nil, nil
	}
	existing, err := lookupExistingIDs(ctx, st, tos)
	if err != nil {
		return nil, err
	}
	sort.Ints(tos)
	for _, to := range tos {
		if !existing[to] {
			return &store.SchemaError{Field: "relations",
				Expect: fmt.Sprintf("target row %d missing", to)}, nil
		}
	}
	return nil, nil
}

type LinkAccrualStore interface {
	InsertKnowledgeLink(ctx context.Context, fromID, toID int, relation string) error
}

func insertKnowledgeLinks(ctx context.Context, st wire.Store, fromID int, specs []linkSpec) error {
	if fromID <= 0 || len(specs) == 0 {
		return nil
	}
	ls, ok := st.(LinkAccrualStore)
	if !ok {
		return fmt.Errorf("write: relations accrual unsupported by store")
	}

	for _, s := range specs {
		if s.To == fromID {
			return fmt.Errorf("write: relations accrual refused: self-referencing link (id=%d)", fromID)
		}
	}
	for _, s := range specs {
		if err := ls.InsertKnowledgeLink(ctx, fromID, s.To, s.Relation); err != nil {
			return fmt.Errorf("write: relations accrual failed: %w", err)
		}
	}
	return nil
}

func withRelations(line string, specs []linkSpec) string {
	if len(specs) == 0 {
		return line
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return line
	}
	arr := make([]any, 0, len(specs))
	for _, s := range specs {
		arr = append(arr, map[string]any{"to": s.To, "relation": s.Relation})
	}
	m["relations"] = arr
	b, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return string(b)
}

type UsageStore interface {
	RecordUsage(ctx context.Context, kind, source string) error
}

type DupIDStore interface {
	DupIDByHash(ctx context.Context, hash string) (int, bool, error)
}

type DupMeta struct {
	ID    int
	Topic string
	State string
}

type DupMetaStore interface {
	DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error)
}

func lookupHash(ctx context.Context, st wire.Store, hash string) (DupMeta, bool, error) {
	if ms, ok := st.(DupMetaStore); ok {
		return ms.DupRowByHash(ctx, hash)
	}
	if ds, ok := st.(DupIDStore); ok {
		id, exists, err := ds.DupIDByHash(ctx, hash)
		return DupMeta{ID: id}, exists, err
	}
	exists, err := st.ExistsHash(ctx, hash)
	return DupMeta{}, exists, err
}

type UsageCountStore interface {
	RecordUsageCount(ctx context.Context, kind, source string, count int) error
}

func RecordUsage(ctx context.Context, st wire.Store, kind, source string) {
	if us, ok := st.(UsageStore); ok {
		_ = us.RecordUsage(ctx, kind, source)
	}
}

func WriteThresholds(ctx context.Context, st wire.Store) store.Thresholds {
	th, err := wire.LoadThresholds(ctx, st)
	if err != nil {
		return store.DefaultThresholds()
	}
	return th
}

func scanEcho(ctx context.Context, st wire.Store, n map[string]any, th store.Thresholds) []EchoCandidate {
	SetEchoThresholds(th.EchoTopK, th.EchoMinScore, th.EchoTopicMatch)
	SetEchoScanBounds(th.SuggestScanCap, th.ConflictDupThreshold)
	topic, _ := n["topic"].(string)
	summary, _ := n["summary"].(string)
	cands, err := EchoScan(ctx, st, topic, summary)
	if err != nil {
		return nil
	}
	return cands
}

func withEcho(line string, cands []EchoCandidate) string {
	if len(cands) == 0 {
		return line
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return line
	}
	arr := make([]any, 0, len(cands))
	for _, c := range cands {
		b, err := json.Marshal(c)
		if err != nil {
			return line
		}
		var cm map[string]any
		if err := json.Unmarshal(b, &cm); err != nil {
			return line
		}
		arr = append(arr, cm)
	}
	m["echo"] = arr
	b, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return string(b)
}

func persistEchoCands(ctx context.Context, st wire.Store, echoID int, cands []EchoCandidate) {
	if echoID <= 0 || len(cands) == 0 {
		return
	}
	rows := make([]db.EchoCandRow, 0, len(cands))
	for _, c := range cands {
		rows = append(rows, db.EchoCandRow{CandidateID: c.ID, Relation: c.Relation, Score: c.Score})
	}
	_ = db.InsertEchoCandidates(ctx, st, echoID, rows)
}

type pendLink struct {
	from int
	spec []linkSpec
}

type writeState struct {
	ctx      context.Context
	st       wire.Store
	stdout   io.Writer
	kind     string
	session  string
	source   string
	payloads []map[string]any
	th       store.Thresholds
	floor    float64

	recs            []map[string]any
	dropped         map[string]bool
	warnings        []string
	correctionQuota int
	capWarned       bool
	hash            string
	state           string
	dups            int
	dupID           int
	dupTopic        string
	dupState        string
	dupEcho         []EchoCandidate
	echoCands       []EchoCandidate
	seen            map[string]bool
	batched         bool
	stagedN         []map[string]any
	stagedH         []string
	stagedL         [][]linkSpec
	stagedK         []string
	recLinks        [][]linkSpec
	recKinds        []string
	pendLinks       []pendLink
	dupLinkSpecs    []linkSpec
}

func RunWrite(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	r, code := parseWriteRequest(ctx, st, args, stdin, stdout)
	if r == nil {
		return code
	}
	if code != 0 {
		return code
	}
	if code := r.validateAndDedupe(); code != 0 {
		return code
	}
	if code := r.resolveStaged(); code != 0 {
		return code
	}
	if code := r.precheckLinks(); code != 0 {
		return code
	}
	return r.persist()
}

func parseWriteRequest(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) (*writeState, int) {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintf(stdout, i18n.T("usage.write")+"\n", sourceList())
			return nil, 0
		}
	}

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--session", Value: true},
			{Name: "--source", Value: true},
		},
		AllowPositional: true,
		DashPositional:  true,
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return nil, 3
	}
	kind, session := "", res.Value("--session")
	if pos := res.Positional(); len(pos) > 0 {
		kind = pos[0]
	}
	source, err := ParseSourceFlag(args)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return nil, 3
	}

	payloads, err := readPayloads(stdin)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return nil, 3
	}
	if len(payloads) == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "payload", Expect: "non-empty JSON/JSONL on stdin"}))
		return nil, 3
	}

	if code := enforceWriteMaxRows(ctx, st, stdout, len(payloads)); code != 0 {
		return nil, code
	}

	th := WriteThresholds(ctx, st)
	r := &writeState{
		ctx: ctx, st: st, stdout: stdout,
		kind: kind, session: session, source: source,
		payloads: payloads, th: th, floor: th.ConfFloor,
		recs: make([]map[string]any, 0, len(payloads)), dropped: map[string]bool{},
		seen: map[string]bool{}, batched: len(payloads) > 1,
		state: "active", correctionQuota: -1,
	}

	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('vocab_tags','vocab_domains')`, nil); err == nil {
		var tags, domains []string
		for _, r2 := range rows {
			if len(r2) != 2 {
				continue
			}
			vals := strings.Split(fmt.Sprint(r2[1]), ",")
			if fmt.Sprint(r2[0]) == "vocab_tags" {
				tags = vals
			} else {
				domains = vals
			}
		}
		store.SetVocabExtras(tags, domains)
	}

	if source == "correction" && kind == "knowledge" {
		r.correctionQuota = th.CorrectionDailyMax - correctionTodayCount(ctx, st)
		if r.correctionQuota < 0 {
			r.correctionQuota = 0
		}
	}
	return r, 0
}

func (r *writeState) finalizeRec(n map[string]any, specs []linkSpec, kval string) {
	if r.correctionQuota == 0 {
		if !r.capWarned {
			r.warnings = append(r.warnings, fmt.Sprintf(
				"correction daily max reached (correction_daily_max=%d) — line skipped", r.th.CorrectionDailyMax))
			r.capWarned = true
		}
		return
	}
	if r.correctionQuota > 0 {
		r.correctionQuota--
	}
	n["kind"] = r.kind
	for k := range n {
		if !kindColumns[r.kind][k] && k != "kind" {
			delete(n, k)
			r.dropped[k] = true
		}
	}
	r.recs = append(r.recs, n)
	r.recLinks = append(r.recLinks, specs)
	r.recKinds = append(r.recKinds, kval)
}

func (r *writeState) validateAndDedupe() int {
	for _, p := range r.payloads {

		if r.source == "correction" && r.kind == "knowledge" {
			applyCorrectionDefaults(p, r.th.CorrectionConfidence)
		}

		if se := validateFieldBytes(p); se != nil {
			fmt.Fprintln(r.stdout, wire.ErrLine(se))
			return 3
		}
		if se := store.Validate(r.kind, p); se != nil {
			fmt.Fprintln(r.stdout, wire.ErrLine(se))
			return 3
		}

		var specs []linkSpec

		kval := ""
		if r.kind == "knowledge" {
			var lse *store.SchemaError
			specs, lse = parseLinkSpecs(p)
			if lse != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(lse))
				return 3
			}

			if cse := validateConditions(p); cse != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(cse))
				return 3
			}

			var kse *store.SchemaError
			kval, kse = parseKnowledgeKind(p)
			if kse != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(kse))
				return 3
			}
			if sse := validateSourcePath(p); sse != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(sse))
				return 3
			}
		}

		r.warnings = append(r.warnings, SanitizePayload(p)...)

		if r.kind == "knowledge" {
			if sp, ok := p["source_path"].(string); ok && strings.TrimSpace(sp) == "" {
				delete(p, "source_path")
			}
		}
		n := store.Normalize(r.kind, p)
		if r.kind == "knowledge" {

			if raw, ok := p["tags"].([]any); ok {
				tags := make([]string, 0, len(raw))
				for _, tag := range raw {
					if s, ok := tag.(string); ok {
						tags = append(tags, s)
					}
				}
				domain, _ := n["domain"].(string)
				for _, w := range store.CheckVocabulary(tags, domain) {
					if strings.HasPrefix(w, rejectedTagPrefix) {
						fmt.Fprintln(r.stdout, wire.ErrLine(&store.SchemaError{Field: "tags", Expect: w}))
						return 3
					}
					r.warnings = append(r.warnings, w)
				}
			}

			h := store.ContentHash(store.HashCanonical(n))

			if r.source == "correction" {
				applyCorrectionOrigin(p, n)
			}
			if r.seen[h] {
				r.dups++
				continue
			}
			r.seen[h] = true
			if r.batched {

				n["content_hash"] = h
				r.stagedN = append(r.stagedN, n)
				r.stagedH = append(r.stagedH, h)
				r.stagedL = append(r.stagedL, specs)
				r.stagedK = append(r.stagedK, kval)
				continue
			}
			existsMeta, exists, err := lookupHash(r.ctx, r.st, h)
			if err != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(err))
				return store.MapExit(err)
			}
			if exists {
				r.dups++
				if len(r.payloads) == 1 {
					r.dupID = existsMeta.ID
					r.dupTopic, r.dupState = existsMeta.Topic, existsMeta.State

					r.dupEcho = scanEcho(r.ctx, r.st, n, r.th)
				}

				if len(specs) > 0 {
					r.pendLinks = append(r.pendLinks, pendLink{from: existsMeta.ID, spec: specs})
					if len(r.payloads) == 1 {
						r.dupLinkSpecs = append(r.dupLinkSpecs, specs...)
					}
				}
				continue
			}
			conf, _ := n["confidence"].(float64)

			r.echoCands = scanEcho(r.ctx, r.st, n, r.th)
			r.state = store.Classify(r.kind, conf, r.floor)
			n["content_hash"] = h
			n["status"] = r.state
			r.hash = h
		}
		if r.kind == "project" {

			name, _ := n["name"].(string)
			if r.seen["project:"+name] {
				r.dups++
				continue
			}
			r.seen["project:"+name] = true
			exists, err := r.st.ExistsProject(r.ctx, name)
			if err != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(err))
				return store.MapExit(err)
			}
			if exists {
				r.dups++
				continue
			}
		}
		n["kind"] = r.kind
		r.finalizeRec(n, specs, kval)
	}
	return 0
}

func (r *writeState) resolveStaged() int {
	if len(r.stagedH) > 0 {
		existing, err := lookupHashBatch(r.ctx, r.st, r.stagedH)
		if err != nil {
			fmt.Fprintln(r.stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		for i, n := range r.stagedN {
			if meta, dup := existing[r.stagedH[i]]; dup {
				r.dups++
				if len(r.stagedL[i]) > 0 {

					r.pendLinks = append(r.pendLinks, pendLink{from: meta.ID, spec: r.stagedL[i]})
				}
				continue
			}
			conf, _ := n["confidence"].(float64)
			r.state = store.Classify(r.kind, conf, r.floor)
			n["status"] = r.state
			r.hash = r.stagedH[i]
			r.finalizeRec(n, r.stagedL[i], r.stagedK[i])
		}
	}
	return 0
}

func (r *writeState) precheckLinks() int {
	var allSpecs []linkSpec
	for _, s := range r.recLinks {
		allSpecs = append(allSpecs, s...)
	}

	for _, pl := range r.pendLinks {
		for _, s := range pl.spec {
			if s.To == pl.from {
				fmt.Fprintln(r.stdout, wire.ErrLine(&store.SchemaError{Field: "relations",
					Expect: fmt.Sprintf("self-referencing link refused (id=%d)", pl.from)}))
				return 3
			}
		}
		allSpecs = append(allSpecs, pl.spec...)
	}
	se, perr := precheckLinkTargets(r.ctx, r.st, allSpecs)
	if se != nil {
		fmt.Fprintln(r.stdout, wire.ErrLine(se))
		return 3
	}
	if perr != nil {
		fmt.Fprintln(r.stdout, wire.ErrLine(perr))
		return store.MapExit(perr)
	}
	return 0
}

func (r *writeState) flushDupLinks(ownCommit bool) int {
	accrued := false
	for _, pl := range r.pendLinks {
		if err := insertKnowledgeLinks(r.ctx, r.st, pl.from, pl.spec); err != nil {
			fmt.Fprintln(r.stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		accrued = true
	}
	if accrued && ownCommit {
		if _, err := r.st.Commit(r.ctx, "knowledge: relations accrual (duplicate resend)"); err != nil {
			fmt.Fprintln(r.stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}
	return 0
}

func (r *writeState) emitDuplicate() int {

	persistEchoCands(r.ctx, r.st, r.dupID, r.dupEcho)
	if code := r.flushDupLinks(true); code != 0 {
		return code
	}
	RecordUsage(r.ctx, r.st, "write", r.source)
	RecordDups(r.ctx, r.st, r.source, r.dups)
	out := store.DuplicateMetaJSON(r.dupID, r.dupTopic, r.dupState)
	out = withRelations(out, r.dupLinkSpecs)
	if len(r.dropped) > 0 || len(r.warnings) > 0 {
		out = withWarnings(out, r.dropped, r.warnings)
	}
	fmt.Fprintln(r.stdout, out)
	return 0
}

func (r *writeState) persist() int {
	if len(r.recs) == 0 {
		return r.emitDuplicate()
	}
	ids, err := r.st.InsertBatch(r.ctx, r.recs)
	if err != nil {
		if !errors.Is(err, store.ErrDuplicate) {
			fmt.Fprintln(r.stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}

		survive := make([]map[string]any, 0, len(r.recs))
		surviveLinks := make([][]linkSpec, 0, len(r.recLinks))
		surviveKinds := make([]string, 0, len(r.recKinds))
		for i, rec := range r.recs {
			specs := r.recLinks[i]
			kval := ""
			if i < len(r.recKinds) {
				kval = r.recKinds[i]
			}
			if rec["kind"] == "project" {

				name, _ := rec["name"].(string)
				exists, err2 := r.st.ExistsProject(r.ctx, name)
				if err2 != nil {
					fmt.Fprintln(r.stdout, wire.ErrLine(err2))
					return store.MapExit(err2)
				}
				if exists {
					r.dups++
					continue
				}
				survive = append(survive, rec)
				surviveLinks = append(surviveLinks, specs)
				surviveKinds = append(surviveKinds, kval)
				continue
			}
			h, _ := rec["content_hash"].(string)
			if h == "" {
				survive = append(survive, rec)
				surviveLinks = append(surviveLinks, specs)
				surviveKinds = append(surviveKinds, kval)
				continue
			}
			existsMeta, exists, err2 := lookupHash(r.ctx, r.st, h)
			if err2 != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(err2))
				return store.MapExit(err2)
			}
			if exists {
				r.dups++
				if len(r.payloads) == 1 {
					r.dupID = existsMeta.ID
					r.dupTopic, r.dupState = existsMeta.Topic, existsMeta.State

					r.dupEcho = r.echoCands
				}
				if len(specs) > 0 {

					r.pendLinks = append(r.pendLinks, pendLink{from: existsMeta.ID, spec: specs})
				}
				continue
			}
			survive = append(survive, rec)
			surviveLinks = append(surviveLinks, specs)
			surviveKinds = append(surviveKinds, kval)
		}
		if len(survive) == 0 {

			return r.emitDuplicate()
		}
		ids, err = r.st.InsertBatch(r.ctx, survive)
		if err != nil {
			if errors.Is(err, store.ErrDuplicate) {

				err = fmt.Errorf("write: batch conflict after dedup recheck: %v", err)
			}
			fmt.Fprintln(r.stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		r.recs = survive
		r.recLinks = surviveLinks
		r.recKinds = surviveKinds
	}

	if len(r.recLinks) > 0 {
		for i, specs := range r.recLinks {
			if i >= len(ids) {
				break
			}
			if err := insertKnowledgeLinks(r.ctx, r.st, ids[i], specs); err != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(err))
				return store.MapExit(err)
			}
		}
	}

	if code := r.flushDupLinks(false); code != 0 {
		return code
	}

	if r.kind == "knowledge" && len(ids) > 0 {
		byKind := map[string][]int{}
		for i := range r.recs {
			if i >= len(ids) || i >= len(r.recKinds) {
				break
			}
			if r.recKinds[i] == "" {
				continue
			}
			byKind[r.recKinds[i]] = append(byKind[r.recKinds[i]], ids[i])
		}
		for _, kv := range knowledgeKindVocabulary {
			group := byKind[kv]
			if len(group) == 0 {
				continue
			}
			if err := db.StampKnowledgeKind(r.ctx, r.st, group, kv); err != nil {
				fmt.Fprintln(r.stdout, wire.ErrLine(err))
				return store.MapExit(err)
			}
		}
	}

	if len(r.payloads) == 1 && r.kind == "knowledge" && len(ids) > 0 {
		persistEchoCands(r.ctx, r.st, ids[0], r.echoCands)
	}

	return r.render(ids)
}

func (r *writeState) render(ids []int) int {
	subject := r.recs[0]["name"]
	if subject == nil {
		subject = r.recs[0]["summary"]
	}
	msg := fmt.Sprintf("%s: %v", r.kind, subject)
	if r.session != "" {
		msg += fmt.Sprintf(" [session:%s]", r.session)
	}
	commitHash, err := r.st.Commit(r.ctx, msg)
	if err != nil {
		fmt.Fprintln(r.stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	var out string
	if len(r.payloads) == 1 {

		id := 0
		if len(ids) > 0 {
			id = ids[0]
		}
		out = store.OutputJSON("committed", r.hash, r.state, id)
		out = withEcho(out, r.echoCands)
	} else {

		out = store.MultiCommitOutput(len(r.recs), r.dups, commitHash)
	}
	if len(r.dropped) > 0 || len(r.warnings) > 0 {
		out = withWarnings(out, r.dropped, r.warnings)
	}
	RecordUsage(r.ctx, r.st, "write", r.source)
	RecordDups(r.ctx, r.st, r.source, r.dups)

	if r.source == "correction" && r.kind == "knowledge" && len(r.recs) > 0 {
		_ = db.BumpDailyFlatKey(r.ctx, r.st, "correction_write", time.Now().UTC().Format("2006-01-02"), len(r.recs))

		nPref, nFact := 0, 0
		for i := range r.recs {
			if i >= len(r.recKinds) {
				break
			}
			switch r.recKinds[i] {
			case "preference":
				nPref++
			case "fact":
				nFact++
			}
		}
		BumpCurationCounter(r.ctx, r.st, "correction_preference", nPref)
		BumpCurationCounter(r.ctx, r.st, "correction_fact", nFact)
	}
	fmt.Fprintln(r.stdout, out)

	if PersistScanStatsFn != nil {
		PersistScanStatsFn(r.ctx, r.st)
	}
	return 0
}

func RecordDups(ctx context.Context, st wire.Store, source string, n int) {
	if n <= 0 {
		return
	}
	if cs, ok := st.(UsageCountStore); ok {
		_ = cs.RecordUsageCount(ctx, "dup", "", n)
		_ = cs.RecordUsageCount(ctx, "dup_"+source, "", n)
		return
	}
	for i := 0; i < n; i++ {
		RecordUsage(ctx, st, "dup", "")
		RecordUsage(ctx, st, "dup_"+source, "")
	}
}

var sanitizeFields = []string{"topic", "summary", "entity", "agent", "name", "session_id", "conditions", "source_path"}

func SanitizePayload(p map[string]any) []string {
	var warns []string

	for _, f := range sanitizeFields {
		s, ok := p[f].(string)
		if !ok {
			continue
		}
		clean := store.SanitizeText(s)
		if clean != s {
			p[f] = clean
		}
		for _, name := range store.ScanSecrets(clean) {
			warns = append(warns, "possible secret: "+name)
		}
	}
	for _, f := range []string{"domain", "origin"} {
		s, ok := p[f].(string)
		if !ok {
			continue
		}
		clean := store.SanitizeText(s)
		if clean != s {
			p[f] = clean
		}
		for _, name := range store.ScanSecrets(clean) {
			warns = append(warns, "possible secret: "+name)
		}
	}
	sanitizeTagElems := func(raw []string) {
		for i, s := range raw {
			clean := store.SanitizeText(s)
			if clean != s {
				raw[i] = clean
			}
			for _, name := range store.ScanSecrets(clean) {
				warns = append(warns, "possible secret: "+name)
			}
		}
	}
	if raw, ok := p["tags"].([]any); ok {
		for i, t := range raw {
			s, ok := t.(string)
			if !ok {
				continue
			}
			clean := store.SanitizeText(s)
			if clean != s {
				raw[i] = clean
			}
			for _, name := range store.ScanSecrets(clean) {
				warns = append(warns, "possible secret: "+name)
			}
		}
	}

	if raw, ok := p["tags"].([]string); ok {
		sanitizeTagElems(raw)
	}
	return warns
}

func withWarnings(line string, dropped map[string]bool, secretWarns []string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return line
	}
	w := append([]string{}, secretWarns...)
	if len(dropped) > 0 {
		keys := make([]string, 0, len(dropped))
		for k := range dropped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w = append(w, "dropped keys: "+strings.Join(keys, ","))
	}
	m["warnings"] = w
	b, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return string(b)
}

const maxPayloadFieldBytes = compactMaxBytes

func validateFieldBytes(p map[string]any) *store.SchemaError {
	for k, v := range p {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if len(s) > maxPayloadFieldBytes {
			return &store.SchemaError{
				Field:  k,
				Expect: fmt.Sprintf("field bytes <= %d", maxPayloadFieldBytes),
			}
		}
	}
	return nil
}

func enforceWriteMaxRows(ctx context.Context, st wire.Store, stdout io.Writer, count int) int {
	max := importMaxRowsLimit(ctx, st)
	if count <= max {
		return 0
	}
	b, err := json.Marshal(map[string]any{
		"error": fmt.Sprintf("write row limit exceeded (export_import_max_rows=%d)", max),
		"max":   max,
		"count": count,
	})
	if err != nil {
		b = []byte(fmt.Sprintf(`{"error":"write row limit exceeded","max":%d,"count":%d}`, max, count))
	}
	fmt.Fprintln(stdout, string(b))
	return 3
}

func readPayloads(stdin io.Reader) ([]map[string]any, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stdin); err != nil {
		return nil, &store.SchemaError{Field: "stdin", Expect: "readable"}
	}
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	var out []map[string]any
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, &store.SchemaError{Field: "payload", Expect: "valid JSON object per line"}
		}
		out = append(out, p)
	}
	return out, nil
}
