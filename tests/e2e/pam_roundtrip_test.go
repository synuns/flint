//go:build e2e

// pam_roundtrip_test.go — v1.4.0 T5: PAM v1.0 내보내기 실DB 왕복 e2e.
// 시나리오: 합성 seed(계보 supersede 1쌍 포함 3행) → export --format pam
// --out → 문서 단언(id "flint:" 규칙·integrity.checksum JCS 재산출 일치·
// relations supersedes 방향 From=신규) → 사이드카 <out>.sha256 존재·sha256
// 일치 → 테이블 정리(fresh 목표 상태) 후 동일 문서를 import --format pam로
// 수입 → 적립 행 수·content·원천 일치·계보 방향 정합(신규 행 오적립 무효화
// 없음). 기존 e2e 헬퍼(mustDB·runCLI·decode·writeKnowledge) 재사용 —
// DB 미기동 시 스킵(e2e_test.go 관례). seed는 합성 데모데이터(개인 데이터
// 금지 — 저장소 원칙).
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

// pamRTMemory — 문서 memory 최소 파싱(id 규칙·계보·본문 단언용).
type pamRTMemory struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Temporal struct {
		SupersededBy string `json:"superseded_by"`
	} `json:"temporal"`
}

// cleanupPAMRoundtrip — seed 토픽(e2e-pam-)과 PAM 수입 파생 토픽
// ("pam[flint:N]") 양쪽 정리(수입은 topic 프리픽스가 달라 별도 조건).
func cleanupPAMRoundtrip(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx(),
		`DELETE FROM knowledge WHERE topic LIKE 'e2e-pam-%' OR topic LIKE 'pam[flint:%'`); err != nil {
		t.Fatalf("PAM 왕복 정리 실패: %v", err)
	}
}

// TestPAMRoundtrip — T5 계약: export → 문서 단언(체크섬·id·relations) →
// 사이드카 → import → 적립 정합. 전 과정 실DB·실CLI 회선.
func TestPAMRoundtrip(t *testing.T) {
	pool := mustDB(t)
	cleanupPAMRoundtrip(t, pool)
	t.Cleanup(func() { cleanupPAMRoundtrip(t, pool) })

	// seed: active a·b + pending c → approve --supersede로 a를 c가 대체.
	// 결과 3행: a invalidated(계보 구), b·c active. pending 잔여가 없게
	// 전부 승격(PAM 비활동 매핑—archived—대상을 계약 밖으로 둔다).
	kA := writeKnowledge(t,
		`{"topic":"e2e-pam-a","summary":"PAM 왕복 프로브 본문 A — 합성 데모.","confidence":0.9,"tags":["e2e-pam"]}`)
	kB := writeKnowledge(t,
		`{"topic":"e2e-pam-b","summary":"PAM 왕복 프로브 본문 B — 합성 데모.","confidence":0.9,"tags":["e2e-pam"]}`)
	kC := writeKnowledge(t,
		`{"topic":"e2e-pam-c","summary":"PAM 왕복 프로브 본문 C — 합성 데모(대체 승격).","confidence":0.7,"tags":["e2e-pam"]}`)
	if kB["state"] != "active" || kC["state"] != "pending" {
		t.Fatalf("seed state kB=%v kC=%v, want active/pending", kB["state"], kC["state"])
	}
	idA, idC := int(kA["id"].(float64)), int(kC["id"].(float64))
	outAppr, code := runCLI(t, []string{"approve", "--id", strconv.Itoa(idC), "--supersede", strconv.Itoa(idA)}, "")
	if code != 0 {
		t.Fatalf("approve --supersede exit=%d, want 0; out=%s", code, outAppr)
	}

	// export — PAM v1.0 문서를 파일로.
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

	// 문서 파싱 — memories는 원시 보존(체크섬 재계산 원천).
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

	// 체크섬 재계산 — memories id 문자열 오름차순 → JCS → SHA-256. 테스트가
	// JCS 라이브러리로 직접 재산출해 문서 값과 일치 단언(T1 계약 — 정렬은
	// id 문자열 기준: 외부 검증자 재현 규칙과 동일, 수치 아님).
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

	// 계보 — relations supersedes 방향: From=신규(c), To=구(a).
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

	// 사이드카 — <out>.sha256 존재·sha256sum 표준 형식·파일 bytes 일치.
	sideRaw, err := os.ReadFile(out + ".sha256")
	if err != nil {
		t.Fatalf("사이드카 미생성: %v", err)
	}
	fileSum := sha256.Sum256(raw)
	if want := hex.EncodeToString(fileSum[:]) + "  " + filepath.Base(out) + "\n"; string(sideRaw) != want {
		t.Fatalf("사이드카=%q, want %q(sha256sum 형식·본문 일치)", sideRaw, want)
	}

	// 수입 — fresh 목표 상태(테이블 정리) 후 동일 문서를 PAM 수입.
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

	// 적립 정합 — 행 수·content·원천(platform flint)·pending 적립.
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
		// 계보 방향 정합 — 구 행(a)은 비적립이라 relation 대상 부재.
		// 방향이 뒤집혔더라도 신규(c)를 오적립 무효화해선 안 된다.
		if invalidAt != nil {
			t.Fatalf("적립 행이 무효화됨(계보 오적용): topic=%q", topic)
		}
	}
	if n != 2 {
		t.Fatalf("PAM 적립 %d건, want 2", n)
	}
}
