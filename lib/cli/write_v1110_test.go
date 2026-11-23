package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/internal/testengine"
)

func runScratch(t *testing.T, args []string, stdin string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out)
	return out.String(), code
}

func TestWriteCorrectionE2ERealSlow(t *testing.T) {
	_, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	freshDSN := testengine.Scratch(t)

	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return newRealStore(ctx, freshDSN)
	}
	t.Cleanup(func() { newStore = prev })

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

	fields := []string{"--fields", "id,topic,confidence,tags"}
	out, code = runScratch(t, append([]string{"read", topic, "--json", "--status", "pending"}, fields...), "")
	if code != 0 {
		t.Fatalf("pending read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, topic) || !strings.Contains(out, "correction") || !strings.Contains(out, "0.7") {
		t.Fatalf("pending 회상=%q, want topic·correction 태그·confidence 0.7", out)
	}

	out, code = runScratch(t, []string{"approve", "--id", fmt.Sprint(id)}, "")
	if code != 0 || !strings.Contains(out, `"status":"approved"`) {
		t.Fatalf("approve exit=%d out=%q, want approved", code, out)
	}

	out, code = runScratch(t, append([]string{"read", topic, "--json"}, fields...), "")
	if code != 0 {
		t.Fatalf("active read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, topic) || !strings.Contains(out, "correction") || !strings.Contains(out, "0.7") {
		t.Fatalf("active 회상=%q, want topic·correction 태그·confidence 0.7(승격 후 active)", out)
	}
}
