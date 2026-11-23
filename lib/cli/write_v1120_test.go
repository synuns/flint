package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
)

func TestGapCandidateE2ERealSlow(t *testing.T) {
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}

	t.Cleanup(base.Close)

	name := fmt.Sprintf("flint_v1120_t3_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() {
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

	out, code := runScratch(t, []string{"write", "knowledge"},
		`{"topic":"합성 시드 지식","summary":"합성 데모 시드 줄","confidence":0.9}`+"\n")
	if code != 0 || !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("시드 적립 exit=%d out=%q", code, out)
	}

	query := strings.Repeat("데모", 45)
	for i := 1; i <= 3; i++ {
		out, code = runScratch(t, []string{"read", query, "--json"}, "")
		if code != 0 {
			t.Fatalf("read %d exit=%d, want 0(제로히트); out=%s", i, code, out)
		}
	}

	fields := []string{"--json", "--fields", "id,topic,summary,confidence,tags", "--status", "pending"}
	out, code = runScratch(t, append([]string{"read", "격차 후보"}, fields...), "")
	if code != 0 {
		t.Fatalf("pending read exit=%d, want 0; out=%s", code, out)
	}
	wantTopic := "격차 후보: " + render.TruncateRunes(knowledge.NormalizeGapQuery(query), 60)
	if !strings.Contains(out, wantTopic) {
		t.Fatalf("pending 회상 토픽 누락: want %q in %q", wantTopic, out)
	}
	if !strings.Contains(out, "회상 실패 3회 — 지식 격차 후보") || !strings.Contains(out, "gap-candidate") || !strings.Contains(out, "0.4") {
		t.Fatalf("pending 회상=%q, want 요약 3회·gap-candidate 태그·confidence 0.4", out)
	}
	id := extractJSONInt(t, out, "id")

	out, code = runScratch(t, []string{"read", query, "--json"}, "")
	if code != 0 {
		t.Fatalf("read 4 exit=%d, want 0; out=%s", code, out)
	}
	out, code = runScratch(t, append([]string{"read", "격차 후보"}, fields...), "")
	if code != 0 {
		t.Fatalf("pending 재회상 exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, "회상 실패 4회 — 지식 격차 후보") {
		t.Fatalf("pending 갱신 실패: %q, want 요약 4회(카운터 리셋 아님·갱신 지속)", out)
	}
	if !strings.Contains(out, wantTopic) {
		t.Fatalf("pending 갱신 후 토픽 변형: %q", out)
	}

	out, code = runScratch(t, []string{"approve", "--id", fmt.Sprint(id)}, "")
	if code != 0 || !strings.Contains(out, `"status":"approved"`) {
		t.Fatalf("approve exit=%d out=%q, want approved", code, out)
	}

	out, code = runScratch(t, []string{"read", "격차 후보", "--json", "--fields", "id,topic,summary,tags"}, "")
	if code != 0 {
		t.Fatalf("active read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, wantTopic) || !strings.Contains(out, "gap-candidate") {
		t.Fatalf("active 회상=%q, want 토픽·gap-candidate 태그", out)
	}

	out, code = runScratch(t, []string{"read", query, "--json"}, "")
	if code != 0 {
		t.Fatalf("read 5 exit=%d, want 0; out=%s", code, out)
	}
	out, code = runScratch(t, []string{"read", "격차 후보", "--json", "--status", "pending", "--fields", "id,topic,summary"}, "")
	if code != 0 || strings.Contains(out, "격차 후보") {
		t.Fatalf("active NOOP 위반: pending 잔존 exit=%d out=%q", code, out)
	}
}

func extractJSONInt(t *testing.T, out, key string) int {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, `"`+key+`":`)
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len(key)+3:])
		end := strings.IndexAny(rest, ",}")
		if end < 0 {
			continue
		}
		cell := strings.Trim(strings.TrimSpace(rest[:end]), `"`)
		var n int
		if _, err := fmt.Sscanf(cell, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	t.Fatalf("id 추출 실패: %q", out)
	return 0
}
