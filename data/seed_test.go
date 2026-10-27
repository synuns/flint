// seed_test.go — 시딩 데이터 검증(fast)과 make seed 멱등(slow, 실 Doltgres).
// 멱등 계약: 해시는 휘발성 필드(valid_at·ts)를 제외한 HashCanonical 경로라
// 동일 payload 재전송은 duplicate·exit 0이어야 한다(spec §7 쓰기 2단계).
package data

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"flint/lib/cli"
	"flint/lib/db"
	"flint/lib/store"
)

// loadSeed — seed.jsonl을 줄 단위 JSON으로 읽는다(원본 텍스트도 함께).
func loadSeed(t *testing.T) ([]map[string]any, []string) {
	t.Helper()
	f, err := os.Open("seed.jsonl")
	if err != nil {
		t.Fatalf("open seed.jsonl: %v", err)
	}
	defer f.Close()
	var maps []map[string]any
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("seed.jsonl 파싱 실패 %q: %v", line, err)
		}
		maps = append(maps, p)
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return maps, lines
}

// TestSeedLinesValid — 모든 줄이 계약 payload 스키마를 준수하고
// 구성(프로젝트 3·결정 5·pending 1·선호 1·플레이북 2)이 플랜 T15·T45와 일치한다.
func TestSeedLinesValid(t *testing.T) {
	maps, _ := loadSeed(t)
	if len(maps) == 0 {
		t.Fatal("seed.jsonl이 비었다")
	}
	projects, decisions, pending, preference, playbooks := 0, 0, 0, 0, 0
	seen := map[string]bool{}
	for _, p := range maps {
		kind, _ := p["kind"].(string)
		if se := store.Validate(kind, p); se != nil {
			t.Fatalf("Validate(%q) 위반: field=%s expect=%s", kind, se.Field, se.Expect)
		}
		key := kind + "|"
		switch kind {
		case "project":
			if p["status"] != "active" {
				t.Errorf("프로젝트 %v: status=%v, want active", p["name"], p["status"])
			}
			key += p["name"].(string)
			projects++
		case "knowledge":
			conf, _ := p["confidence"].(float64)
			hasTag := func(want string) bool {
				for _, tag := range p["tags"].([]any) {
					if tag == want {
						return true
					}
				}
				return false
			}
			switch {
			case hasTag("playbook"):
				playbooks++
			case p["domain"] == "preference":
				preference++
			case conf < 0.8:
				pending++
			default:
				decisions++
			}
			key += p["topic"].(string)
		default:
			t.Fatalf("알 수 없는 kind %q", kind)
		}
		if seen[key] {
			t.Errorf("중복 키 시딩: %s", key)
		}
		seen[key] = true
	}
	if projects != 3 {
		t.Errorf("활성 프로젝트 수 = %d, want 3", projects)
	}
	if decisions != 5 {
		t.Errorf("기획 결정(confidence>=0.8) 수 = %d, want 5", decisions)
	}
	if pending != 1 {
		t.Errorf("pending 시연(confidence 0.7) 수 = %d, want 1", pending)
	}
	if preference != 1 {
		t.Errorf("사용자 선호(domain=preference) 수 = %d, want 1", preference)
	}
	if playbooks != 2 {
		t.Errorf("플레이북(tag=playbook) 수 = %d, want 2", playbooks)
	}
}

// TestSeedIdempotentV4 — v0.4 플레이북 시딩 2건(절차 지식, CONTENT.md §1a·§4).
// 계약: summary는 조건→단계→완료조건 3단, 어휘표 경고 0, confidence 0.9.
func TestSeedIdempotentV4(t *testing.T) {
	maps, _ := loadSeed(t)
	playbooks := map[string]map[string]any{}
	for _, p := range maps {
		if p["kind"] != "knowledge" {
			continue
		}
		for _, tag := range p["tags"].([]any) {
			if tag == "playbook" {
				playbooks[p["topic"].(string)] = p
			}
		}
	}
	for _, topic := range []string{"Doltgres 충돌 유도 절차", "flint 백업 복구 절차"} {
		p, ok := playbooks[topic]
		if !ok {
			t.Fatalf("플레이북 시딩 누락: %q", topic)
		}
		var tags []string
		for _, tag := range p["tags"].([]any) {
			tags = append(tags, tag.(string))
		}
		if w := store.CheckVocabulary(tags, p["domain"].(string)); len(w) != 0 {
			t.Errorf("%q 어휘 경고: %v", topic, w)
		}
		summary := p["summary"].(string)
		if strings.Count(summary, "→") < 2 {
			t.Errorf("%q summary 계약 위반(조건→단계→완료조건): %q", topic, summary)
		}
		if c, _ := p["confidence"].(float64); c != 0.9 {
			t.Errorf("%q confidence = %v, want 0.9", topic, c)
		}
	}
}

// seedDSN — FLINT_DSN 우선, 없으면 기본(ADR-0001).
func seedDSN() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	return "postgresql://flint:flint-dev@localhost:5433/flint"
}

// seedSharedDBLock — 공유 Doltgres DB를 쓰는 패키지 간 상호 배제(파일
// 잠금). go test ./...는 패키지 바이너리를 병렬로 띄우므로 잠금 없이는
// lib/db newTestRepo의 knowledge 전체 DELETE가 이 테스트의 1차 시딩과
// 2차 시딩(멱등 계약 검증) 사이에 끼어, 2차가 hash 미발견 신규 insert
// (status committed)로 멱등 계약이 깨진다(CI run 39716547383, 로컬 재현
// ./data ./lib/db -count=3 -p 2 3/3 red). 잠금 파일 경로는 lib/db
// sharedDBLock·lib/backup drillLock과 동일해야 상호 배제가 성립한다
// (v0.5 선례 e1704b8).
func seedSharedDBLock(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "flint-drill.lock"),
		os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("잠금 파일: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatalf("flock: %v", err)
	}
	t.Cleanup(func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	})
}

// TestSeedIdempotent — 시딩 2회 실행 시 2회째는 전부 duplicate·exit 0.
// 실 Doltgres 필요: 서버가 없으면 스킵(exit 2 철학 — 조용히 진행).
func TestSeedIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, seedDSN())
	if err != nil {
		t.Skipf("doltgres 미기동, 스킵: %v", err)
	}
	pool.Close()
	seedSharedDBLock(t)

	_, lines := loadSeed(t)
	byKind := map[string][]string{}
	var order []string
	for _, line := range lines {
		var p map[string]any
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("파싱: %v", err)
		}
		kind := p["kind"].(string)
		if _, ok := byKind[kind]; !ok {
			order = append(order, kind)
		}
		byKind[kind] = append(byKind[kind], line)
	}

	seedOnce := func() (int, []string) {
		worst := 0
		var outs []string
		for _, kind := range order {
			var buf bytes.Buffer
			code := cli.Run([]string{"write", kind},
				strings.NewReader(strings.Join(byKind[kind], "\n")+"\n"), &buf)
			if code > worst {
				worst = code
			}
			outs = append(outs, strings.TrimSpace(buf.String()))
		}
		return worst, outs
	}

	// 1회째: 신규 DB면 committed, 이미 시딩된 DB면 duplicate — 둘 다 exit 0.
	if code, outs := seedOnce(); code != 0 {
		t.Fatalf("첫 시딩 exit=%d outputs=%v", code, outs)
	}
	// 2회째: 전부 duplicate·exit 0(멱등 계약). duplicate 경로도 경고를
	// 병행 출력하므로(v0.9.2 #6) status 필드로 판정한다.
	code, outs := seedOnce()
	if code != 0 {
		t.Fatalf("재시딩 exit=%d, want 0", code)
	}
	for i, out := range outs {
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Errorf("재시딩 %d번째 출력 파싱 실패: %q", i+1, out)
			continue
		}
		if m["status"] != "duplicate" {
			t.Errorf("재시딩 %d번째 status = %v, want duplicate (out=%q)", i+1, m["status"], out)
		}
	}
}
