package doclint

import (
	"strings"
	"testing"
)

const writeBoundaryDoc = "docs/contracts/write-boundary.md"

type boundaryAnchor struct {
	file string
	mark string
}

var writeBoundaryAnchors = []boundaryAnchor{
	{"lib/store/sanitize.go", "func SanitizeText(s string) string {"},
	{"lib/store/sanitize.go", "func replaceMarkerFold(s string) string {"},
	{"lib/knowledge/write.go", "var sanitizeFields = []string{"},
	{"lib/knowledge/write.go", "func SanitizePayload(p map[string]any) []string {"},
	{"lib/knowledge/write.go", "func maybeAccrueGapCandidate(ctx context.Context, st wire.Store, sanitizedQuery string, count int64) {"},
	{"lib/knowledge/read.go", "maybeAccrueGapCandidate(ctx, st, q, n)"},
	{"lib/render/render.go", "func Text(s string) string {"},
	{"lib/render/render.go", "func Masked(s string) string {"},
	{"lib/render/render.go", "func MaskedStrict(s string) string {"},
	{"lib/store/pipeline.go", "func Classify(kind string, conf, floor float64) string {"},
	{"lib/knowledge/pending.go", "type pendingVerdict struct {"},
	{"lib/knowledge/approve.go", "func RunApprove("},
}

func TestWriteBoundaryDoc(t *testing.T) {
	doc := docStr(t, writeBoundaryDoc)

	pos := 0
	for _, m := range []string{
		"`SanitizePayload`",
		"`sanitizeFields`",
		"`SanitizeText`",
		"`replaceMarkerFold`",
		"`render.Text`",
		"`[flint]`",
		"`pendingVerdict`",
		"`RunApprove`",
		"`store.Classify`",
		"`lib/query/read.go`",
		"`maybeAccrueGapCandidate`",
		"`NormalizeGapQuery`",
		"`gap_daily_max`",
	} {
		i := strings.Index(doc[pos:], m)
		if i < 0 {
			t.Fatalf("경계 문서에 실물 함수 마커 없음(순서 포함): %s", m)
		}
		pos += i
	}

	for _, a := range writeBoundaryAnchors {
		src := sourceFile(t, a.file)
		if !strings.Contains(src, a.mark) {
			t.Fatalf("문서가 참조하는 실물 마커 소실(드리프트): %s :: %s", a.file, a.mark)
		}
	}

	for _, want := range []string{
		"no unverified promotion",
		"secondary mitigation",
		"read-only",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("경계 문서에 계약 어휘 누락: %q", want)
		}
	}
}

func TestWriteBoundaryQueryOnly(t *testing.T) {
	src := sourceFile(t, "lib/query/read.go")
	for _, forbidden := range []string{"INSERT", "UPDATE ", "DELETE"} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("query 입구가 쓰기 구문 포함 — 문서의 read-only 계약 드리프트: %s", forbidden)
		}
	}
}
