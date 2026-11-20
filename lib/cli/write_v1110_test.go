// write_v1110_test.go — v1.11.0 T2: the --source correction end-to-end
// (real-DB slow, an isolated scratch database — the root_test.go fresh-DB
// convention). Contract: a correction write without confidence lands
// pending with the correction_confidence default (0.7) and the
// "correction" tag; approve promotes it; read recalls the tag·confidence
// on the active view. Every stage is the existing circuit (write→pending→
// approve→read) — the correction source adds defaults only.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

// runScratch — runs one cli Run round against the scratch DSN (the
// newStore seam is swapped by the caller's helper).
func runScratch(t *testing.T, args []string, stdin string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out)
	return out.String(), code
}

// TestWriteCorrectionE2ERealSlow — slow (SUPERLITE_REAL=1 +
// FLINT_TEST_DSN): on a fresh scratch DB, write --source correction
// (confidence omitted) → single-line committed·state=pending·id captured
// → read --status pending recalls tag "correction"·confidence 0.7 →
// approve --id N → read (the default active view) recalls the same row
// (the promotion to active demonstrated end-to-end).
func TestWriteCorrectionE2ERealSlow(t *testing.T) {
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	// Close via t.Cleanup registered BEFORE the DROP below — Cleanup is
	// LIFO, so the DROP (registered later) runs while the pool is still
	// open (a `defer base.Close()` would run first and the DROP would hit
	// a closed pool, silently leaking the scratch DB).
	t.Cleanup(base.Close)

	name := fmt.Sprintf("flint_v1110_t2_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort DROP with a short retry — right after Run's store
		// Close, Doltgres may still hold the session and reject the drop
		// (a leftover scratch DB is droppable once the process exits).
		var last error
		for i := 0; i < 3; i++ {
			if _, last = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name); last == nil {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Logf("스크래치 DB 잔존(%q): %v", name, last)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	freshDSN := u.String()

	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return newRealStore(ctx, freshDSN)
	}
	t.Cleanup(func() { newStore = prev })

	// 1. Accrual — confidence omitted: committed·pending·the correction defaults.
	const topic = "v1110-t2-e2e-correction"
	stdin := fmt.Sprintf(`{"topic":%q,"summary":"합성 데모 교정 줄"}`+"\n", topic)
	out, code := runScratch(t, []string{"write", "knowledge", "--source", "correction"}, stdin)
	if code != 0 {
		t.Fatalf("적립 exit=%d, want 0; out=%s", code, out)
	}
	var w map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &w); err != nil {
		t.Fatalf("적립 출력 파싱: %v — %s", err, out)
	}
	if w["status"] != "committed" || w["state"] != "pending" {
		t.Fatalf("적립 출력=%v, want committed/pending(기본 0.7 < floor)", w)
	}
	idf, ok := w["id"].(float64)
	if !ok || idf < 1 {
		t.Fatalf("id=%v, want 1 이상", w["id"])
	}
	id := int(idf)

	// 2. Pending recall — the tag·confidence of the stored row.
	fields := []string{"--fields", "id,topic,confidence,tags"}
	out, code = runScratch(t, append([]string{"read", topic, "--json", "--status", "pending"}, fields...), "")
	if code != 0 {
		t.Fatalf("pending read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, topic) || !strings.Contains(out, "correction") || !strings.Contains(out, "0.7") {
		t.Fatalf("pending 회상=%q, want topic·correction 태그·confidence 0.7", out)
	}

	// 3. Promotion — the existing approve circuit.
	out, code = runScratch(t, []string{"approve", "--id", fmt.Sprint(id)}, "")
	if code != 0 || !strings.Contains(out, `"status":"approved"`) {
		t.Fatalf("approve exit=%d out=%q, want approved", code, out)
	}

	// 4. Post-promotion active recall — the default active view exposes the row.
	out, code = runScratch(t, append([]string{"read", topic, "--json"}, fields...), "")
	if code != 0 {
		t.Fatalf("active read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, topic) || !strings.Contains(out, "correction") || !strings.Contains(out, "0.7") {
		t.Fatalf("active 회상=%q, want topic·correction 태그·confidence 0.7(승격 후 active)", out)
	}
}
