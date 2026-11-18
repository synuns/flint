// history_test.go — LogEntries(wire.LogQuery 계약의 Doltgres 구현) slow
// 실DB 회귀. 커밋 3개(합성 데모 지식 — 비telemetry 2·telemetry 1)를 심고
// Hash·Prefix·BeforeOrAt·Earliest·LatestN(+MessageNotLike) 모드를 실측한다.
// 포팅 쿼리는 원문 보존(asof.go·audit.go 이식)이라 컬럼 충원 범위도 원문
// 승계: Hash·LatestN은 4열, Prefix·BeforeOrAt은 commit_hash만, Earliest는 date만.
package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// seedT3Commit — 합성 데모 지식 1건 적립 후 마커 메시지로 dolt 커밋.
// 커밋 해시(빈 해시 = 스테이징 0행)를 돌려준다.
func seedT3Commit(t *testing.T, r *Repo, marker string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v0180-t3-" + marker)}); err != nil {
		t.Fatalf("InsertBatch(%s): %v", marker, err)
	}
	// tel 마커는 'telemetry: ' 접두 커밋으로 — MessageNotLike 제외가
	// 실제로 매칭되는지 검증한다(공회전 단정 방지).
	msg := "flint v0180 t3 demo " + marker
	if marker == "tel" {
		msg = "telemetry: v0180 t3 demo tel"
	}
	h, err := r.Commit(ctx, msg)
	if err != nil {
		t.Fatalf("Commit(%s): %v", marker, err)
	}
	if h == "" {
		t.Fatalf("Commit(%s): 빈 해시(스테이징 0행)", marker)
	}
	return h
}

// TestLogEntriesSlowModes — 5모드 실측. 커밋 시각 구분을 위해 커밋 사이
// 1.1s 대기(dolt_log date 초 단위 절단 대비). 공유 DB에는 타 테스트 커밋이
// 남지만 flock 직렬화로 본 테스트 커밋이 최신이고, LatestN 검증은
// 비telemetry 최신 2건이 본 테스트 커밋임에 의존한다.
func TestLogEntriesSlowModes(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	h1 := seedT3Commit(t, r, "one")
	time.Sleep(1100 * time.Millisecond)
	h2 := seedT3Commit(t, r, "two")
	time.Sleep(1100 * time.Millisecond)
	htel := seedT3Commit(t, r, "tel") // LatestN 기본 필터 회피 검증용(아래 msg 참조)

	// ── Hash 정확 일치 — 4열(해시·작성자·시각·메시지) 충원. ──
	e2, err := r.LogEntries(ctx, wire.LogQuery{Hash: h2})
	if err != nil {
		t.Fatalf("LogEntries(Hash): %v", err)
	}
	if len(e2) != 1 || e2[0].Hash != h2 {
		t.Fatalf("LogEntries(Hash) = %v, want 해시 %s 1행", e2, h2)
	}
	if e2[0].Committer == "" {
		t.Fatalf("LogEntries(Hash) committer 비어 있음: %+v", e2[0])
	}
	if e2[0].Message != "flint v0180 t3 demo two" {
		t.Fatalf("LogEntries(Hash) message = %q, want 커밋 메시지", e2[0].Message)
	}
	if e2[0].Date.IsZero() {
		t.Fatalf("LogEntries(Hash) date 제로값: %+v", e2[0])
	}
	e1, err := r.LogEntries(ctx, wire.LogQuery{Hash: h1})
	if err != nil {
		t.Fatalf("LogEntries(Hash h1): %v", err)
	}
	if len(e1) != 1 || e1[0].Date.IsZero() {
		t.Fatalf("LogEntries(Hash h1) = %v, want date 충원 1행", e1)
	}

	// ── Prefix — LIKE 일치 전체 목록(유일성 판정은 호출측). ──
	prefix := h2
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	ps, err := r.LogEntries(ctx, wire.LogQuery{Prefix: prefix})
	if err != nil {
		t.Fatalf("LogEntries(Prefix): %v", err)
	}
	found := false
	for _, e := range ps {
		if !strings.HasPrefix(e.Hash, prefix) {
			t.Fatalf("LogEntries(Prefix) 비접두 해시 %q (prefix %q)", e.Hash, prefix)
		}
		if e.Hash == h2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("LogEntries(Prefix %q) 목록에 %s 없음: %v", prefix, h2, ps)
	}
	zs, err := r.LogEntries(ctx, wire.LogQuery{Prefix: "qqqqqqv0180t3"})
	if err != nil {
		t.Fatalf("LogEntries(Prefix 미발견): %v", err)
	}
	if len(zs) != 0 {
		t.Fatalf("LogEntries(미발견 prefix) = %v, want 0행", zs)
	}

	// ── BeforeOrAt — 시각 직전(포함) 최신 커밋 1행. ──
	bs, err := r.LogEntries(ctx, wire.LogQuery{BeforeOrAt: e2[0].Date})
	if err != nil {
		t.Fatalf("LogEntries(BeforeOrAt): %v", err)
	}
	if len(bs) != 1 || bs[0].Hash != h2 {
		t.Fatalf("LogEntries(BeforeOrAt) = %v, want h2 1행(tel 커밋은 1.1s 뒤라 제외)", bs)
	}

	// ── Earliest — 첫 커밋 시각(commit_hash 열 없음 — 원문 쿼리 승계). ──
	ee, err := r.LogEntries(ctx, wire.LogQuery{Earliest: true})
	if err != nil {
		t.Fatalf("LogEntries(Earliest): %v", err)
	}
	if len(ee) != 1 || ee[0].Date.IsZero() {
		t.Fatalf("LogEntries(Earliest) = %v, want date 1행", ee)
	}
	if ee[0].Date.After(e1[0].Date) {
		t.Fatalf("LogEntries(Earliest) date %v > 첫 데모 커밋 %v", ee[0].Date, e1[0].Date)
	}

	// ── LatestN + MessageNotLike — DESC·LIMIT·telemetry 제외. ──
	ls, err := r.LogEntries(ctx, wire.LogQuery{LatestN: 2, MessageNotLike: "telemetry: %"})
	if err != nil {
		t.Fatalf("LogEntries(LatestN+NotLike): %v", err)
	}
	if len(ls) != 2 || ls[0].Hash != h2 || ls[1].Hash != h1 {
		t.Fatalf("LogEntries(LatestN 2) = %v, want [h2, h1] DESC", ls)
	}
	for _, e := range ls {
		if strings.HasPrefix(e.Message, "telemetry: ") {
			t.Fatalf("LogEntries(LatestN) telemetry 커밋 누출: %+v", e)
		}
	}
	// 필터 없는 LatestN — 최신 커밋은 telemetry 마커 커밋.
	l1, err := r.LogEntries(ctx, wire.LogQuery{LatestN: 1})
	if err != nil {
		t.Fatalf("LogEntries(LatestN 필터 없음): %v", err)
	}
	if len(l1) != 1 || l1[0].Hash != htel || l1[0].Message != "telemetry: v0180 t3 demo tel" {
		t.Fatalf("LogEntries(LatestN 1 무필터) = %v, want tel 커밋", l1)
	}

	// ── LatestN + MessageLike — 포함 필터 단독(audit --session 이관용). ──
	ms, err := r.LogEntries(ctx, wire.LogQuery{LatestN: 10, MessageLike: "%v0180 t3 demo%"})
	if err != nil {
		t.Fatalf("LogEntries(LatestN+Like): %v", err)
	}
	likeHas := map[string]bool{}
	for _, e := range ms {
		if !strings.Contains(e.Message, "v0180 t3 demo") {
			t.Fatalf("LogEntries(Like) 마커 외 메시지 누출: %+v", e)
		}
		likeHas[e.Hash] = true
	}
	if !likeHas[htel] || !likeHas[h1] || !likeHas[h2] {
		t.Fatalf("LogEntries(Like) 데모 커밋 3종 미포함: %v", ms)
	}

	// ── LatestN + MessageNotLike + MessageLike — AND 결합(telemetry 제외+포함). ──
	cs, err := r.LogEntries(ctx, wire.LogQuery{
		LatestN:        10,
		MessageNotLike: "telemetry: %",
		MessageLike:    "%v0180 t3 demo%",
	})
	if err != nil {
		t.Fatalf("LogEntries(LatestN+NotLike+Like): %v", err)
	}
	combHas := map[string]bool{}
	for _, e := range cs {
		if strings.HasPrefix(e.Message, "telemetry: ") {
			t.Fatalf("LogEntries(결합) telemetry 커밋 누출: %+v", e)
		}
		if !strings.Contains(e.Message, "v0180 t3 demo") {
			t.Fatalf("LogEntries(결합) 마커 외 메시지 누출: %+v", e)
		}
		combHas[e.Hash] = true
	}
	if !combHas[h1] || !combHas[h2] || combHas[htel] {
		t.Fatalf("LogEntries(결합) = h1·h2만 포함해야 함(htel 제외): %v", cs)
	}

	// ── 빈 질의 — 모드 미지정은 오류. ──
	if _, err := r.LogEntries(ctx, wire.LogQuery{}); err == nil {
		t.Fatal("LogEntries(빈 질의) 오류 없음, want 오류")
	}
}
