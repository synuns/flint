package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type ConflictStore interface {
	ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error)
}

func maintainRunDupSuspects(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainDupSuspects(ctx, r.ms, stdout, r.dupLimit)
}

func maintainRunConflictSuspects(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	ms, dupLimit := r.ms, r.dupLimit
	cs, ok := ms.(ConflictStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}
	return maintainConflictSuspects(ctx, cs, stdout, dupLimit)
}

func maintainRunMerge(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	ms, from, to := r.ms, r.from, r.to
	if from == 0 || to == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --merge requires --from and --to"))))
		return 1
	}

	recallAuditDisposed(ctx, ms, []int{from})
	if err := ms.Merge(ctx, from, to); err != nil {
		return adminErrLine(stdout, err)
	}
	fmt.Fprintf(stdout, `{"status":"merged","from":%d,"to":%d}`+"\n", from, to)
	return 0
}

func maintainRunMergeAudit(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainMergeAudit(ctx, r.ms, stdout, r.limit)
}
func maintainDupSuspects(ctx context.Context, ms MaintStore, stdout io.Writer, limit int) int {
	groups, err := ms.DupGroups(ctx, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, DupSuspectsJSON(groups))
	return 0
}

func DupSuspectsJSON(groups []db.DupGroup) string {
	type dupGroupJSON struct {
		Topic string `json:"topic"`
		IDs   []int  `json:"ids"`
	}
	out := struct {
		Status string         `json:"status"`
		Groups []dupGroupJSON `json:"groups"`
	}{Status: "dup-suspects", Groups: make([]dupGroupJSON, 0, len(groups))}
	for _, g := range groups {
		ids := g.IDs
		if ids == nil {
			ids = []int{}
		}
		out.Groups = append(out.Groups, dupGroupJSON{Topic: render.Text(g.Topic), IDs: ids})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"status":"dup-suspects","groups":[]}`
	}
	return string(b)
}

var ConflictDupThreshold = store.DefaultThresholds().ConflictDupThreshold

const SuspectsDefaultLimit = 10

const MaintainTUILoadLimit = 500

func ConflictDice(a, b string) float64 {
	return query.BigramDiceMultiset(a, b)
}

func ClassifyConflict(summA, summB string) bool {
	return classifyConflictAt(summA, summB, ConflictDupThreshold)
}

func classifyConflictAt(summA, summB string, dupThreshold float64) bool {
	return ConflictDice(summA, summB) < dupThreshold
}

func ConflictSummaryPrefix(s string) string {
	return render.TruncateRunes(s, 40)
}

func IsConflictGroup(g db.ConflictGroup, dupThreshold float64) bool {
	for i := 0; i < len(g.Summaries); i++ {
		for j := i + 1; j < len(g.Summaries); j++ {
			if classifyConflictAt(g.Summaries[i], g.Summaries[j], dupThreshold) {
				return true
			}
		}
	}
	return false
}

func maintainConflictSuspects(ctx context.Context, cs ConflictStore, stdout io.Writer, limit int) int {
	groups, err := cs.ConflictGroups(ctx, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	th := store.DefaultThresholds()
	if ms, ok := cs.(wire.Store); ok {
		if loaded, lErr := wire.LoadThresholds(ctx, ms); lErr == nil {
			th = loaded
		}
	}
	out := make([]db.ConflictGroup, 0, len(groups))
	for _, g := range groups {
		if IsConflictGroup(g, th.ConflictDupThreshold) {
			out = append(out, g)
		}
	}
	fmt.Fprintln(stdout, conflictSuspectsJSON(out))
	return 0
}

func conflictSuspectsJSON(groups []db.ConflictGroup) string {
	type conflictGroupJSON struct {
		Topic     string   `json:"topic"`
		IDs       []int    `json:"ids"`
		Summaries []string `json:"summaries"`
	}
	out := struct {
		Status string              `json:"status"`
		Groups []conflictGroupJSON `json:"groups"`
	}{Status: "conflict-suspects", Groups: make([]conflictGroupJSON, 0, len(groups))}
	for _, g := range groups {
		ids := g.IDs
		if ids == nil {
			ids = []int{}
		}
		sums := make([]string, 0, len(g.Summaries))
		for _, s := range g.Summaries {
			sums = append(sums, ConflictSummaryPrefix(render.Text(s)))
		}
		out.Groups = append(out.Groups, conflictGroupJSON{Topic: render.Text(g.Topic), IDs: ids, Summaries: sums})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"status":"conflict-suspects","groups":[]}`
	}
	return string(b)
}
func maintainMergeAudit(ctx context.Context, ms wire.Store, stdout io.Writer, limit int) int {
	th := store.DefaultThresholds()
	if loaded, lErr := wire.LoadThresholds(ctx, ms); lErr == nil {
		th = loaded
	}
	if th.SuggestScanCap <= 0 {
		th.SuggestScanCap = store.DefaultThresholds().SuggestScanCap
	}
	if th.ConflictDupThreshold <= 0 {
		th.ConflictDupThreshold = store.DefaultThresholds().ConflictDupThreshold
	}
	if limit <= 0 {
		limit = 20
	}
	rows, truncated, err := knowledge.FetchMergeAuditRows(ctx, ms, th.SuggestScanCap)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	pairs, cost := knowledge.ClassifyMergeAuditPairs(rows, th.ConflictDupThreshold)
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}

	knowledge.BumpCurationCounter(ctx, ms, "merge_audit_pairs", len(pairs))

	getKV, _ := MetaKV(ctx, ms)
	echoStats, _ := ScanStatsFromMetaFn(getKV)
	out, mErr := json.Marshal(struct {
		Status        string                     `json:"status"`
		Threshold     float64                    `json:"threshold"`
		ScanCap       int                        `json:"scan_cap"`
		Scanned       int                        `json:"scanned"`
		ScanTruncated bool                       `json:"scan_truncated"`
		Cost          knowledge.MergeAuditCost   `json:"cost"`
		EchoP95US     int                        `json:"echo_scan_p95_us"`
		Count         int                        `json:"count"`
		Items         []knowledge.MergeAuditPair `json:"items"`
		Summary       string                     `json:"summary"`
	}{Status: "merge-audit", Threshold: th.ConflictDupThreshold, ScanCap: th.SuggestScanCap,
		Scanned: len(rows), ScanTruncated: truncated, Cost: cost, EchoP95US: echoStats.P95,
		Count: len(pairs), Items: pairs,
		Summary: fmt.Sprintf("%d duplicate pair(s) — dispose via maintain --merge/--supersede (approval required)",
			len(pairs))})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}
