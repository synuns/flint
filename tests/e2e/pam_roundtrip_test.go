//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	jc "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

type pamRTMemory struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Temporal struct {
		SupersededBy string `json:"superseded_by"`
	} `json:"temporal"`
}

func cleanupPAMRoundtrip(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx(),
		`DELETE FROM knowledge WHERE topic LIKE 'e2e-pam-%' OR topic LIKE 'pam[flint:%'`); err != nil {
		t.Fatalf("PAM 왕복 정리 실패: %v", err)
	}
}

func TestPAMRoundtrip(t *testing.T) {
	pool := mustDB(t)
	cleanupPAMRoundtrip(t, pool)
	t.Cleanup(func() { cleanupPAMRoundtrip(t, pool) })

	kA := writeKnowledge(t,
		`{"topic":"e2e-pam-a","summary":"PAM 왕복 프로브 본문 A — 합성 데모.","confidence":0.9,"claim":"procedure","tags":["e2e-pam"]}`)
	kB := writeKnowledge(t,
		`{"topic":"e2e-pam-b","summary":"PAM 왕복 프로브 본문 B — 합성 데모.","confidence":0.9,"claim":"lesson","tags":["e2e-pam"]}`)
	kC := writeKnowledge(t,
		`{"topic":"e2e-pam-c","summary":"PAM 왕복 프로브 본문 C — 합성 데모(대체 승격).","confidence":0.7,"tags":["e2e-pam"]}`)
	if kB["state"] != "active" || kC["state"] != "pending" {
		t.Fatalf("seed state kB=%v kC=%v, want active/pending", kB["state"], kC["state"])
	}
	idA, idC := int(kA["id"].(float64)), int(kC["id"].(float64))
	outAppr, code := runCLI(t, []string{"approve", "--id", strconv.Itoa(idC), "--supersede", strconv.Itoa(idA), "--claim", "procedure"}, "")
	if code != 0 {
		t.Fatalf("approve --supersede exit=%d, want 0; out=%s", code, outAppr)
	}

	out := filepath.Join(t.TempDir(), "memory-store.json")
	outExp, code := runCLI(t, []string{
		"export", "--format", "pam", "--status", "all", "--tag", "e2e-pam", "--out", out,
	}, "")
	if code != 0 {
		t.Fatalf("export --format pam exit=%d, want 0; out=%s", code, outExp)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("export 파일 읽기: %v", err)
	}

	var doc struct {
		Schema        string            `json:"schema"`
		SchemaVersion string            `json:"schema_version"`
		Memories      []json.RawMessage `json:"memories"`
		Relations     []struct {
			Type string `json:"type"`
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"relations"`
		Integrity struct {
			Checksum      string `json:"checksum"`
			TotalMemories int    `json:"total_memories"`
		} `json:"integrity"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("PAM 문서 파싱: %v", err)
	}
	if doc.Schema != "portable-ai-memory" || doc.SchemaVersion != "1.0" {
		t.Fatalf("schema=%q/%q, want portable-ai-memory/1.0", doc.Schema, doc.SchemaVersion)
	}
	if len(doc.Memories) != 3 {
		t.Fatalf("memories %d건, want 3(계보 포함 seed); memories=%s", len(doc.Memories), doc.Memories)
	}
	idRe := regexp.MustCompile(`^flint:[0-9]+$`)
	parseMem := func(b json.RawMessage) pamRTMemory {
		var m pamRTMemory
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("memory 파싱: %v: %s", err, b)
		}
		return m
	}
	byID := map[string]pamRTMemory{}
	for _, rm := range doc.Memories {
		m := parseMem(rm)
		if !idRe.MatchString(m.ID) {
			t.Fatalf("memory id 규칙 위반: %q, want ^flint:[0-9]+$", m.ID)
		}
		byID[m.ID] = m
	}
	if doc.Integrity.TotalMemories != len(doc.Memories) {
		t.Fatalf("total_memories=%d, want %d", doc.Integrity.TotalMemories, len(doc.Memories))
	}

	sorted := append([]json.RawMessage(nil), doc.Memories...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return parseMem(sorted[i]).ID < parseMem(sorted[j]).ID
	})
	arr, err := json.Marshal(sorted)
	if err != nil {
		t.Fatalf("memories 재직렬화: %v", err)
	}
	canon, err := jc.Transform(arr)
	if err != nil {
		t.Fatalf("JCS 변환: %v", err)
	}
	sum := sha256.Sum256(canon)
	if want := "sha256:" + hex.EncodeToString(sum[:]); doc.Integrity.Checksum != want {
		t.Fatalf("integrity.checksum=%q, want 재계산 %q(불일치)", doc.Integrity.Checksum, want)
	}

	if len(doc.Relations) != 1 {
		t.Fatalf("relations %d건, want 1(supersede 1쌍): %+v", len(doc.Relations), doc.Relations)
	}
	rel := doc.Relations[0]
	if rel.Type != "supersedes" || rel.From != "flint:"+strconv.Itoa(idC) || rel.To != "flint:"+strconv.Itoa(idA) {
		t.Fatalf("relation=%+v, want supersedes From=flint:%d(신규) To=flint:%d(구)", rel, idC, idA)
	}
	if m := byID["flint:"+strconv.Itoa(idA)]; m.Status != "superseded" || m.Temporal.SupersededBy != "flint:"+strconv.Itoa(idC) {
		t.Fatalf("구 행 memory=%+v, want status=superseded superseded_by=flint:%d", m, idC)
	}

	sideRaw, err := os.ReadFile(out + ".sha256")
	if err != nil {
		t.Fatalf("사이드카 미생성: %v", err)
	}
	fileSum := sha256.Sum256(raw)
	if want := hex.EncodeToString(fileSum[:]) + "  " + filepath.Base(out) + "\n"; string(sideRaw) != want {
		t.Fatalf("사이드카=%q, want %q(sha256sum 형식·본문 일치)", sideRaw, want)
	}

	cleanupPAMRoundtrip(t, pool)
	outImp, code := runCLI(t, []string{"import", "--format", "pam", out}, "")
	if code != 0 {
		t.Fatalf("import --format pam exit=%d, want 0; out=%s", code, outImp)
	}
	m := decode(t, outImp)
	if m["status"] != "ok" || m["imported"] != float64(2) {
		t.Fatalf("PAM import 요약=%v, want status=ok imported=2(active b·c)", m)
	}
	if sk, _ := m["skipped"].(float64); sk != 1 {
		t.Fatalf("skipped=%v, want 1(superseded 구 행 a — 비적립 보고)", m["skipped"])
	}

	rows, err := pool.Query(ctx(),
		`SELECT topic, summary, status, origin, invalid_at FROM knowledge WHERE topic LIKE 'pam[flint:%' ORDER BY topic`)
	if err != nil {
		t.Fatalf("적립 행 조회 실패: %v", err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		var topic, summary, status, origin string
		var invalidAt any
		if err := rows.Scan(&topic, &summary, &status, &origin, &invalidAt); err != nil {
			t.Fatalf("적립 행 스캔 실패: %v", err)
		}
		n++
		if !strings.Contains(summary, "합성 데모") {
			t.Fatalf("적립 content 불일치: %q", summary)
		}
		if !strings.HasPrefix(topic, "pam[flint:") {
			t.Fatalf("적립 topic=%q, want pam[flint:N] 파생", topic)
		}
		if status != "pending" || origin != "import:pam:flint" {
			t.Fatalf("적립 status/origin=%q/%q, want pending/import:pam:flint", status, origin)
		}

		if invalidAt != nil {
			t.Fatalf("적립 행이 무효화됨(계보 오적용): topic=%q", topic)
		}
	}
	if n != 2 {
		t.Fatalf("PAM 적립 %d건, want 2", n)
	}
}
