//go:build e2e

// e2e — 실 Doltgres 대상 T11 3케이스. 실행: go test -tags e2e ./tests/e2e/
// (make e2e). DB 미기동 시 전체 스킵. 테이블 정리는 DELETE FROM 기반
// (Doltgres는 TRUNCATE RESTART IDENTITY 미지원, T7 인시던트).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/cli"
	"flint/lib/db"
	"flint/lib/mcp"
	"flint/lib/sqlfs"
)

// baseDSN — ADR-0001: 포트 5433.
const baseDSN = "postgresql://flint:flint-dev@localhost:5433/flint"

func dsn() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	return baseDSN
}

// mustDB — 실서버 접속 불가 시 스킵(로컬 패스트 스위트 보호).
// 뷰는 최신 embed SQL로 동기화: Migrate가 schema_version=1 게이트라
// 구버전 뷰가 남은 DB에서도 e2e가 현행 코드 기준으로 돌게 한다.
// Doltgres는 다중 문장 Exec를 조용히 파손하고 CREATE OR REPLACE가
// 확장 프로토콜에서 컬럼 목록을 갱신하지 않으므로, simple protocol로
// DROP+CREATE를 문장별 실행한다.
func mustDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	syncDSN := dsn() + "?default_query_exec_mode=simple_protocol"
	if strings.Contains(dsn(), "?") {
		syncDSN = dsn() + "&default_query_exec_mode=simple_protocol"
	}
	pool, err := db.Connect(context.Background(), syncDSN)
	if err != nil {
		t.Skipf("Doltgres 미기동: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(),
		`DROP VIEW IF EXISTS active_knowledge, hot_view`); err != nil {
		t.Fatalf("뷰 드롭 실패: %v", err)
	}
	for _, stmt := range statements(sqlfs.ViewsSQL()) {
		if _, err := pool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("뷰 재생성 실패: %v", err)
		}
	}
	return pool
}

// statements — SQL 덩어리를 문장별로 분해. 선행 '--' 주석 줄은 제거.
func statements(sqlText string) []string {
	var out []string
	for _, stmt := range strings.Split(sqlText, ";") {
		var lines []string
		for _, l := range strings.Split(stmt, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "--") {
				continue
			}
			lines = append(lines, l)
		}
		if s := strings.TrimSpace(strings.Join(lines, "\n")); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cleanupTopics — e2e 마커 프리픽스 행 정리.
func cleanupTopics(t *testing.T, pool *pgxpool.Pool, prefix string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM knowledge WHERE topic LIKE $1`, prefix+"%"); err != nil {
		t.Fatalf("정리 실패(%s): %v", prefix, err)
	}
}

// countTopic — 해당 topic 행 수.
func countTopic(t *testing.T, pool *pgxpool.Pool, topic string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM knowledge WHERE topic = $1`, topic).Scan(&n); err != nil {
		t.Fatalf("count 실패: %v", err)
	}
	return n
}

// runCLI — cli.Run 직접 호출(바이너리 대신, 플랜 T11 접근).
func runCLI(t *testing.T, args []string, stdin string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := cli.Run(args, strings.NewReader(stdin), &out)
	return out.String(), code
}

// decode — 한 줄 JSON 출력 파싱.
func decode(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("출력이 JSON 아님: %q: %v", out, err)
	}
	return m
}

// --- 케이스 ①: dedup 멱등 — 동일 payload 2회 write ---

func TestDedupIdenticalPayloadTwice(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-dedup-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-dedup-") })

	payload := `{"topic":"e2e-dedup-k","summary":"dedup 멱등 프로브","confidence":0.9,"tags":["e2e"]}`

	out1, code1 := runCLI(t, []string{"write", "knowledge"}, payload)
	if code1 != 0 {
		t.Fatalf("1회째 exit=%d, want 0; out=%s", code1, out1)
	}
	if m := decode(t, out1); m["status"] != "committed" {
		t.Fatalf("1회째 status=%v, want committed; out=%s", m["status"], out1)
	}

	out2, code2 := runCLI(t, []string{"write", "knowledge"}, payload)
	if code2 != 0 {
		t.Fatalf("2회째 exit=%d, want 0(중복도 성공); out=%s", code2, out2)
	}
	if got := strings.TrimSpace(out2); got != `{"status":"duplicate"}` {
		t.Fatalf("2회째 출력=%q, want {\"status\":\"duplicate\"}", got)
	}

	if n := countTopic(t, pool, "e2e-dedup-k"); n != 1 {
		t.Fatalf("행 수=%d, want 1(멱등 위반)", n)
	}
}

// --- 케이스 ②: 재시도 — 커밋 일시 실패 후 0s/1s/3s 재시도로 성공 ---
//
// Doltgres는 LOCK TABLE·pg_terminate_backend 미지원이라 SQL 수준 충돌 유도가
// 불가하다(실험 기록). 대신 로컬 TCP 프록시로 dolt_add 첫 전송만 차단해
// 실제 네트워크 일시 실패를 만들고, 실재 Retry(기본 0s/1s/3s)를 경유시킨다.

func TestRetryRecoversAfterTransientCommitFailure(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-retry-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-retry-") })

	backend := backendAddr(t)
	proxy := newKillProxy(t, backend)
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@"+proxy.Addr().String()+"/flint")

	payload := `{"topic":"e2e-retry-k","summary":"재시도 회복 프로브","confidence":0.9}`
	start := time.Now()
	out, code := runCLI(t, []string{"write", "knowledge"}, payload)
	elapsed := time.Since(start)

	if code != 0 {
		t.Fatalf("exit=%d, want 0(재시도 후 성공); out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "committed" {
		t.Fatalf("status=%v, want committed; out=%s", m["status"], out)
	}
	// 첫 시도 실패 → 1초 지연 후 재시도 성공이므로 최소 0.9s 경과.
	if elapsed < 900*time.Millisecond {
		t.Fatalf("경과 %v: 재시도 지연(0s/1s/3s) 경유 증거 부족", elapsed)
	}
	if n := countTopic(t, pool, "e2e-retry-k"); n != 1 {
		t.Fatalf("행 수=%d, want 1", n)
	}
}

// backendAddr — DSN에서 host:port 추출.
func backendAddr(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(dsn())
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	return u.Host
}

// killProxy — 첫 dolt_add 요청 바이트만 서버 전달 없이 연결을 끊는 TCP 프록시.
// 이후 재접속은 그대로 통과시켜 재시도가 성공하게 한다.
type killProxy struct {
	ln     net.Listener
	mu     sync.Mutex
	killed bool
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

func newKillProxy(t *testing.T, backend string) *killProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("프록시 리슨: %v", err)
	}
	p := &killProxy{ln: ln, conns: make(map[net.Conn]struct{})}
	t.Cleanup(func() {
		ln.Close()
		p.mu.Lock()
		for c := range p.conns {
			c.Close()
		}
		p.mu.Unlock()
		p.wg.Wait()
	})
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				p.serve(c, backend)
			}()
		}
	}()
	return p
}

func (p *killProxy) Addr() net.Addr { return p.ln.Addr() }

func (p *killProxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
}

func (p *killProxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
	c.Close()
}

func (p *killProxy) serve(c net.Conn, backend string) {
	p.track(c)
	s, err := net.Dial("tcp", backend)
	if err != nil {
		p.untrack(c)
		return
	}
	p.track(s)
	done := make(chan struct{})
	go func() { p.pipe(s, c, false); close(done) }()
	p.pipe(c, s, true)
	<-done
	p.untrack(c)
	p.untrack(s)
}

// pipe — src→dst 복사. scan이면 클라→서버 흐름에서 "dolt_add"를 감시해
// 최초 1회만 그 바이트를 서버에 보내지 않고 연결을 종료한다(일시 실패).
func (p *killProxy) pipe(src, dst net.Conn, scan bool) {
	defer src.Close()
	defer dst.Close()
	var carry []byte
	buf := make([]byte, 8192)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			data := buf[:n]
			if scan {
				carry = append(carry, data...)
				if bytes.Contains(carry, []byte("dolt_add")) {
					p.mu.Lock()
					first := !p.killed
					p.killed = true
					p.mu.Unlock()
					if first {
						return // 전달 없이 종료 → 이 요청만 실패
					}
					carry = nil
				}
				if len(carry) > 16 {
					carry = carry[len(carry)-16:]
				}
			}
			if _, werr := dst.Write(data); werr != nil {
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}

// --- 케이스 ③: 이스케이프 — %, _, ' 가 와일드카드/주입 없이 리터럴 취급 ---

func TestReadEscapesLikeWildcards(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-esc-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-esc-") })

	jsonl := strings.Join([]string{
		`{"topic":"e2e-esc-k1","summary":"progress 100% done","confidence":0.9}`,
		`{"topic":"e2e-esc-k2","summary":"progress 100x done","confidence":0.9}`,
		`{"topic":"e2e-esc-k3","summary":"snake_case name","confidence":0.9}`,
		`{"topic":"e2e-esc-k4","summary":"snakexcase name","confidence":0.9}`,
		`{"topic":"e2e-esc-k5","summary":"o'brien quote","confidence":0.9}`,
	}, "\n")
	out, code := runCLI(t, []string{"write", "knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("씨딩 exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "committed" {
		t.Fatalf("status=%v, want committed; out=%s", m["status"], out)
	}

	assertRead := func(q, wantTopic string, wantLines int) {
		t.Helper()
		out, code := runCLI(t, []string{"read", q}, "")
		if code != 0 {
			t.Fatalf("read %q exit=%d, want 0; out=%s", q, code, out)
		}
		var lines []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) != wantLines {
			t.Fatalf("read %q 결과 %d건, want %d건; out=%s", q, len(lines), wantLines, out)
		}
		if wantTopic != "" && !strings.Contains(lines[0], wantTopic) {
			t.Fatalf("read %q 첫 줄에 %q 없음: %s", q, wantTopic, lines[0])
		}
	}

	assertRead("100%", "e2e-esc-k1", 1)       // % 리터럴: 100x(k2) 오탐 차단
	assertRead("snake_case", "e2e-esc-k3", 1) // _ 리터럴: snakexcase(k4) 오탐 차단
	assertRead("o'brien", "e2e-esc-k5", 1)    // 작은따옴표 파라미터 전달
	assertRead(`%' OR '1'='1`, "", 0)         // 주입 시도: 오류 없이 빈 결과
}

// --- v0.2 섹션: approve·maintain·done·백업 자동(T24) ---

// setMeta / delMeta — meta 키-값 설정·복구(임계값·last_backup 프로브).
// 테스트 끝나면 키를 지워 다른 테스트가 기본값을 보게 한다.
func setMeta(t *testing.T, pool *pgxpool.Pool, key, value string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		t.Fatalf("meta 설정 실패(%s): %v", key, err)
	}
}

func delMeta(t *testing.T, pool *pgxpool.Pool, key string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM meta WHERE key = $1`, key); err != nil {
		t.Fatalf("meta 삭제 실패(%s): %v", key, err)
	}
}

// writeKnowledge — CLI write 회선(공용). committed JSON을 돌려준다.
func writeKnowledge(t *testing.T, payload string) map[string]any {
	t.Helper()
	out, code := runCLI(t, []string{"write", "knowledge"}, payload)
	if code != 0 {
		t.Fatalf("write exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" {
		t.Fatalf("write status=%v, want committed; out=%s", m["status"], out)
	}
	return m
}

// readLines — read <query> 결과 줄 수.
func readLines(t *testing.T, query string) []string {
	t.Helper()
	out, code := runCLI(t, []string{"read", query}, "")
	if code != 0 {
		t.Fatalf("read %q exit=%d, want 0; out=%s", query, code, out)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// 케이스 ④를 제외한 v0.2 시나리오 — ① 승격 후 read 즉시 반영.
func TestApprovePromotesPendingToActive(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-appr-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-appr-") })

	// confidence 0.7 < 0.8 → pending 게이트(spec §8).
	m := writeKnowledge(t,
		`{"topic":"e2e-appr-k","summary":"승격 프로브","confidence":0.7}`)
	if m["state"] != "pending" {
		t.Fatalf("state=%v, want pending", m["state"])
	}
	id := int(m["id"].(float64))

	// 승인 전 read에는 status=pending으로 보인다(active 뷰는 무효만 차단).
	readStatus := func() string {
		t.Helper()
		out, code := runCLI(t, []string{"read", "e2e-appr-k", "--json", "--fields", "id,status"}, "")
		if code != 0 {
			t.Fatalf("read exit=%d, want 0; out=%s", code, out)
		}
		m := decode(t, out)
		s, _ := m["status"].(string)
		return s
	}
	if s := readStatus(); s != "pending" {
		t.Fatalf("승인 전 read status=%q, want pending", s)
	}

	out, code := runCLI(t, []string{"approve", "--id", strconv.Itoa(id)}, "")
	if code != 0 {
		t.Fatalf("approve exit=%d, want 0; out=%s", code, out)
	}
	if am := decode(t, out); am["status"] != "approved" || am["id"] != float64(id) {
		t.Fatalf("approve 출력=%v, want approved/%d", am, id)
	}

	// Review Focus(state): 승격 직후 read에 반영.
	if s := readStatus(); s != "active" {
		t.Fatalf("승인 후 read status=%q, want active", s)
	}
}

// ② maintain --check 임계값 보고 — 경계(최대값과 같으면 ok, 초과분부터 경보).
func TestMaintainCheckThresholdBoundary(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-mnt-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-mnt-") })

	// DB에 남아 있는 다른 pending 행과 무관하게 판정되게 최대값을
	// 현재 pending 수 + 2로 맞춘다(경계: +2건은 ok, +3건은 review-needed).
	var base int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL`).
		Scan(&base); err != nil {
		t.Fatalf("baseline pending 수 실패: %v", err)
	}
	setMeta(t, pool, "pending_max", strconv.Itoa(base+2))
	t.Cleanup(func() { delMeta(t, pool, "pending_max") })
	setMeta(t, pool, "dup_susp_max", "1000000") // 본 케이스 밖 요인 차단
	t.Cleanup(func() { delMeta(t, pool, "dup_susp_max") })

	writeKnowledge(t, `{"topic":"e2e-mnt-k1","summary":"경계 1","confidence":0.7}`)
	writeKnowledge(t, `{"topic":"e2e-mnt-k2","summary":"경계 2","confidence":0.7}`)
	out, code := runCLI(t, []string{"maintain", "--check"}, "")
	if code != 0 {
		t.Fatalf("maintain --check exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("경계(== max) status=%v, want ok; out=%s", m["status"], out)
	}

	writeKnowledge(t, `{"topic":"e2e-mnt-k3","summary":"경계 3","confidence":0.7}`)
	out, code = runCLI(t, []string{"maintain", "--check"}, "")
	if code != 0 {
		t.Fatalf("maintain --check exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "review-needed" || m["pending"] != float64(base+3) {
		t.Fatalf("초과 status=%v pending=%v, want review-needed/%d; out=%s",
			m["status"], m["pending"], base+3, out)
	}
}

// ③ done — 미기록 연속 2는 ok, 3부터 stale 경고(둘 다 exit 0).
func TestDoneWarnsConsecutiveUnrecordedSessions(t *testing.T) {
	pool := mustDB(t)
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM sessions WHERE agent = $1`, "e2e-done-agent"); err != nil {
		t.Fatalf("sessions 정리 실패: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM sessions WHERE agent = $1`, "e2e-done-agent")
	})
	setMeta(t, pool, "stale_sessions", "3")
	t.Cleanup(func() { delMeta(t, pool, "stale_sessions") })
	// done이 자동 백업을 동반하므로 홈 디렉터리 대신 임시 디렉터리로 격리.
	t.Setenv("FLINT_BACKUP_DIR", t.TempDir())

	latestSessionID := func() string {
		t.Helper()
		var id int
		if err := pool.QueryRow(context.Background(),
			`SELECT max(id) FROM sessions WHERE agent = $1`, "e2e-done-agent").Scan(&id); err != nil {
			t.Fatalf("session id 조회 실패: %v", err)
		}
		return strconv.Itoa(id)
	}

	// 세션 2개(knowledge 기록 없음) — 임계값 직전이라 ok.
	out, code := runCLI(t, []string{"write", "session"},
		`{"agent":"e2e-done-agent","summary":"미기록 1"}`+"\n"+
			`{"agent":"e2e-done-agent","summary":"미기록 2"}`+"\n")
	if code != 0 {
		t.Fatalf("session write exit=%d, want 0; out=%s", code, out)
	}
	out, code = runCLI(t, []string{"done", "--session", latestSessionID()}, "")
	if code != 0 {
		t.Fatalf("done exit=%d, want 0(종료 불가); out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("연속 2 status=%v, want ok; out=%s", m["status"], out)
	}

	// 3개째 — stale 경고.
	out, code = runCLI(t, []string{"write", "session"},
		`{"agent":"e2e-done-agent","summary":"미기록 3"}`+"\n")
	if code != 0 {
		t.Fatalf("session write exit=%d, want 0; out=%s", code, out)
	}
	out, code = runCLI(t, []string{"done", "--session", latestSessionID()}, "")
	if code != 0 {
		t.Fatalf("done exit=%d, want 0(경고여도 종료 가능); out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "stale" || m["runs"] != float64(3) {
		t.Fatalf("연속 3 출력=%v, want stale/3", m)
	}
	if w, _ := m["warning"].(string); !strings.Contains(w, "연속 3세션 미기록") {
		t.Fatalf("warning=%q, want 연속 3세션 미기록", w)
	}
}

// ④ 백업 자동 — last_backup 25시간 전이면 다음 done에서 덤프 파일 생성.
func TestDoneTriggersAutoBackupAfter24h(t *testing.T) {
	pool := mustDB(t)
	stale := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	setMeta(t, pool, "last_backup", stale)
	t.Cleanup(func() { delMeta(t, pool, "last_backup") })

	dir := t.TempDir()
	t.Setenv("FLINT_BACKUP_DIR", dir)

	out, code := runCLI(t, []string{"done", "--session", "e2e-backup-probe"}, "")
	if code != 0 {
		t.Fatalf("done exit=%d, want 0; out=%s", code, out)
	}
	decode(t, out) // 한 줄 JSON 유지

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("자동 백업 파일 미생성(entries=%d, err=%v)", len(entries), err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			t.Fatalf("백업 파일 %s 비어 있음(size=0)", e.Name())
		}
	}

	// meta 갱신 — 다음 세션에서 재백업하지 않게.
	var last string
	if err := pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'last_backup'`).Scan(&last); err != nil {
		t.Fatalf("last_backup 조회 실패: %v", err)
	}
	if last == stale {
		t.Fatalf("last_backup 미갱신: %q", last)
	}
}

// --- v0.3 섹션: MCP 왕복·hot 계측·version 빌드(T34) ---

// mcpHandle — MCP 메서드 3종 조립. tools/call은 DispatchTool(cli.Run 실구현)
// 을 경유해 실DB·exit 계약을 그대로 재사용한다(바이너리의 mcp 모드와 동일 구성).
func mcpHandle(method string, params map[string]any) (any, error) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
		}, nil
	case "tools/list":
		return map[string]any{"tools": mcp.ToolsManifest()}, nil
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		out, code := mcp.DispatchTool(name, args)
		return map[string]any{
			"content":  []map[string]any{{"type": "text", "text": out}},
			"exitCode": code,
		}, nil
	default:
		return nil, mcp.ErrUnknownMethod
	}
}

// ① MCP stdio 왕복 — initialize→tools/list(3도구)→tools/call flint_read 실DB.
// Review Focus(protocol): 응답 id 대응, MCP 경로도 실DB 계약·예산 준수.
func TestMCPRoundtripRealDB(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-mcp-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-mcp-") })

	// 실DB 씨딩 — flint_read가 찾을 대상(도구 밖 CLI 회선으로 심는다).
	writeKnowledge(t, `{"topic":"e2e-mcp-k","summary":"MCP 왕복 프로브","confidence":0.9}`)

	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"flint_read","arguments":{"query":"e2e-mcp-k","limit":5}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := mcp.Serve(strings.NewReader(reqs), &out, mcpHandle); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var resps []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l != "" {
			resps = append(resps, decode(t, l))
		}
	}
	if len(resps) != 3 {
		t.Fatalf("응답 %d행, want 3(요청마다 1행); out=%s", len(resps), out.String())
	}
	for i, m := range resps {
		if m["id"] != float64(i+1) {
			t.Fatalf("응답[%d] id=%v, want %d(id 대응); out=%s", i, m["id"], i+1, out.String())
		}
		if _, bad := m["error"]; bad {
			t.Fatalf("응답[%d] error=%v; out=%s", i, m["error"], out.String())
		}
	}

	// tools/list — 도구 3종·이름 정합.
	tools, _ := resps[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("도구 %d건, want 3; out=%s", len(tools), out.String())
	}
	got := map[string]bool{}
	for _, tl := range tools {
		if td, ok := tl.(map[string]any); ok {
			got[td["name"].(string)] = true
		}
	}
	for _, want := range []string{"flint_read", "flint_write", "flint_hot"} {
		if !got[want] {
			t.Fatalf("tools/list에 %q 없음; out=%s", want, out.String())
		}
	}

	// tools/call flint_read — 실DB 경유로 씨딩 토픽 회신(exit 0).
	result, _ := resps[2]["result"].(map[string]any)
	if ec, _ := result["exitCode"].(float64); ec != 0 {
		t.Fatalf("flint_read exitCode=%v, want 0; out=%s", result["exitCode"], out.String())
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("flint_read content 비어 있음; out=%s", out.String())
	}
	if text, _ := content[0].(map[string]any)["text"].(string); !strings.Contains(text, "e2e-mcp-k") {
		t.Fatalf("flint_read 결과에 씨딩 토픽 없음: %q", text)
	}
}

// ② hot 계측 — flint_hot 1회 후 status에 hot_calls≥1·hot_p95 노출.
func TestHotMetricsShowInStatus(t *testing.T) {
	mustDB(t) // 서버 기동 게이트(status가 DB를 본다)

	if _, code := runCLI(t, []string{"hot"}, ""); code != 0 {
		t.Fatalf("hot exit=%d, want 0", code)
	}
	out, code := runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if calls, _ := m["hot_calls"].(float64); calls < 1 {
		t.Fatalf("hot_calls=%v, want >=1; out=%s", m["hot_calls"], out)
	}
	if _, ok := m["hot_p95"]; !ok {
		t.Fatalf("status에 hot_p95 미노출; out=%s", out)
	}
}

// ③ version — make build 후 바이너리가 잘못된 DSN에서도 exit 0·한 줄 JSON.
func TestVersionBinaryJSON(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	build := exec.Command("make", "build")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build 실패: %v; out=%s", err, b)
	}

	vcmd := exec.Command(filepath.Join(root, "bin", "flint"), "version")
	vcmd.Env = append(os.Environ(), "FLINT_DSN=postgresql://flint:wrong@127.0.0.1:1/flint")
	b, err := vcmd.Output()
	if err != nil {
		t.Fatalf("version exit 오류(store-free여야 함): %v; out=%s", err, b)
	}
	m := decode(t, string(b))
	if v, _ := m["version"].(string); v == "" {
		t.Fatalf("version 필드 비어 있음: %s", b)
	}
	if c, _ := m["commit"].(string); c == "" {
		t.Fatalf("commit 필드 비어 있음: %s", b)
	}
}

// ④ schema 승격 실측 — v1 DB에서 done 1회(=세션 시작 경유) 실행만으로
// meta가 승격된다(EnsureSchemaV2 → EnsureSchemaV3 연쇄, newRealStore 훅·T57
// 배선). 최종 버전은 '3'(v2 컬럼 + v3 인덱스), 승격 후에도 read·status 정상.
func TestSchemaV2PromotedAfterDone(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-v3-")
	t.Cleanup(func() {
		cleanupTopics(t, pool, "e2e-v3-")
		// 후속 실행·다른 status 테스트 보호: 버전을 '1'로 되돌린다
		// (승격 경로는 IF NOT EXISTS 기반이라 재승격 멱등).
		setMeta(t, pool, "schema_version", "1")
	})
	// 백업 자동 격리(done이 하루 첫 세션 백업을 돌릴 수 있다).
	t.Setenv("FLINT_BACKUP_DIR", t.TempDir())

	// v1 상태에서 시작(실DB가 이미 승격됐어도 이 테스트는 '1'에서 출발).
	setMeta(t, pool, "schema_version", "1")
	writeKnowledge(t, `{"topic":"e2e-v3-k","summary":"v2 승격 프로브","confidence":0.9}`)

	out, code := runCLI(t, []string{"done", "--session", "e2e-v3-probe"}, "")
	if code != 0 {
		t.Fatalf("done exit=%d, want 0; out=%s", code, out)
	}

	var version string
	if err := pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 조회 실패: %v", err)
	}
	if version != "3" {
		t.Fatalf("done 후 schema_version=%q, want 3(v2→v3 연쇄 승격)", version)
	}

	// 승격 후 read 정상 — 씨딩 토픽이 그대로 보인다.
	lines := readLines(t, "e2e-v3-k")
	if len(lines) == 0 || !strings.Contains(lines[0], "e2e-v3-k") {
		t.Fatalf("승격 후 read 결과 이상: %v", lines)
	}

	// 회귀: v3에서도 status는 exit 0(호환 허용 집합 {"1","2","3"}).
	out, code = runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("v3 status exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["schema_version"] != "3" {
		t.Fatalf("status schema_version=%v, want 3; out=%s", m["schema_version"], out)
	}
}

// --- v0.4 섹션: 어휘 경고·패턴 증류·사용 로그·계측 누적·setup(T44) ---

// ① 어휘 밖 tag write — CONTENT.md §4 표 밖 tag("bogus")는 경고만
// 반환되고 저장은 거부되지 않는다(exit 0·committed).
func TestWriteWarnsUnknownTag(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-vocab-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-vocab-") })

	out, code := runCLI(t, []string{"write", "knowledge"},
		`{"topic":"e2e-vocab-k","summary":"어휘 경고 프로브","confidence":0.9,"tags":["playbook","bogus"]}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(경고는 저장 거부 아님); out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" {
		t.Fatalf("status=%v, want committed; out=%s", m["status"], out)
	}
	found := false
	for _, w := range toStrings(m["warnings"]) {
		if strings.Contains(w, "unknown tag: bogus") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings에 %q 없음: %v", "unknown tag: bogus", m["warnings"])
	}
}

// toStrings — JSON 디코딩된 any 슬라이스를 문자열 슬라이스로.
func toStrings(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ② 패턴 증류 — 동일 topic 성공 knowledge 2건 → repeat-success 후보
// 식별(증거 2건). db.PatternCandidates 직접 경유(maintain 제안 소스).
func TestPatternRepeatSuccessCandidate(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-pat-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-pat-") })

	// tags 명시 — NULL tags는 LIKE 집계에서 제외되므로 성공으로 세려면 필요.
	writeKnowledge(t, `{"topic":"e2e-pat-k","summary":"성공 1","confidence":0.9,"tags":["process"]}`)
	writeKnowledge(t, `{"topic":"e2e-pat-k","summary":"성공 2","confidence":0.9,"tags":["process"]}`)

	repo, err := db.NewRepo(context.Background(), dsn())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	pats, err := repo.PatternCandidates(context.Background())
	if err != nil {
		t.Fatalf("PatternCandidates: %v", err)
	}
	for _, p := range pats {
		if p.Topic != "e2e-pat-k" {
			continue
		}
		if p.Kind != "repeat-success" {
			t.Fatalf("kind=%q, want repeat-success", p.Kind)
		}
		if len(p.Evidence) != 2 {
			t.Fatalf("증거 %d건, want 2: %v", len(p.Evidence), p.Evidence)
		}
		return
	}
	t.Fatalf("후보에 %q 없음: %+v", "e2e-pat-k", pats)
}

// ③ 플레이북 사용 로그 — 3회 중 2성공 → PlaybookStats Rate 0.667.
// 로그는 meta pb_<id>_uses/_ok 증분(신규 테이블 없음, schema v2 유지).
func TestPlaybookUseStatsRate(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-usage-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-usage-") })

	m := writeKnowledge(t,
		`{"topic":"e2e-usage-k","summary":"사용 로그 프로브","confidence":0.9,"tags":["playbook"]}`)
	id := int(m["id"].(float64))
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			`DELETE FROM meta WHERE key = $1 OR key = $2`,
			fmtPBKey(id, "uses"), fmtPBKey(id, "ok"))
	})

	repo, err := db.NewRepo(context.Background(), dsn())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	for _, ok := range []bool{true, true, false} {
		if err := repo.LogPlaybookUse(context.Background(), id, ok); err != nil {
			t.Fatalf("LogPlaybookUse(%v): %v", ok, err)
		}
	}
	stats, err := repo.PlaybookStats(context.Background())
	if err != nil {
		t.Fatalf("PlaybookStats: %v", err)
	}
	for _, s := range stats {
		if s.ID != id {
			continue
		}
		if s.Uses != 3 || s.OK != 2 {
			t.Fatalf("uses/ok = %d/%d, want 3/2", s.Uses, s.OK)
		}
		if s.Rate != 0.667 {
			t.Fatalf("rate = %v, want 0.667", s.Rate)
		}
		return
	}
	t.Fatalf("stats에 id=%d 없음: %+v", id, stats)
}

// fmtPBKey — 플레이북 사용 로그 meta 키 조립.
func fmtPBKey(id int, suffix string) string {
	return "pb_" + strconv.Itoa(id) + "_" + suffix
}

// ④ 계측 프로세스 분리 누적 — 별도 프로세스 hot 2회 실행 뒤 status가
// meta hot_stats baseline을 읽어 hot_calls=2로 보고(T38 wiring 실측).
func TestHotCallsAccumulateAcrossProcesses(t *testing.T) {
	pool := mustDB(t)
	delMeta(t, pool, "hot_stats") // 직전 실행 잔여와 격리
	t.Cleanup(func() { delMeta(t, pool, "hot_stats") })

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	bin := filepath.Join(root, "bin", "flint")
	build := exec.Command("make", "build")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build 실패: %v; out=%s", err, b)
	}
	for i := 0; i < 2; i++ {
		hot := exec.Command(bin, "hot")
		hot.Env = os.Environ()
		if b, err := hot.CombinedOutput(); err != nil {
			t.Fatalf("hot %d회차 실패: %v; out=%s", i+1, err, b)
		}
	}
	out, code := runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if calls, _ := m["hot_calls"].(float64); calls != 2 {
		t.Fatalf("hot_calls=%v, want 2(프로세스 분리 누적); out=%s", m["hot_calls"], out)
	}
}

// ⑤ setup --dry — 단계 4건만 출력(exec·쿼리 없음)·exit 0.
func TestSetupDrySteps(t *testing.T) {
	out, code := runCLI(t, []string{"setup", "--dry"}, "")
	if code != 0 {
		t.Fatalf("setup --dry exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "dry" {
		t.Fatalf("status=%v, want dry; out=%s", m["status"], out)
	}
	if steps := toStrings(m["steps"]); len(steps) != 4 {
		t.Fatalf("단계 %d건, want 4; out=%s", len(steps), out)
	}
}

// --- v0.5 섹션: mcp 실 프로세스·감사 필터·로테이션·복구 훈련·랭킹(T57) ---

// rebuildBin — make build 후 bin/flint 경로 반환(실 프로세스 테스트 공용).
func rebuildBin(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	build := exec.Command("make", "build")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build 실패: %v; out=%s", err, b)
	}
	return filepath.Join(root, "bin", "flint")
}

// ① mcp 실 프로세스 왕복 — bin/flint mcp가 stdin JSON-RPC(initialize→
// flint_read)에 순서대로 응답하고 EOF에 exit 0으로 종료한다(T49 배선 실측:
// main.go blank import가 없으면 not linked로 즉사 → 이 테스트가 RED).
func TestMCPRealProcessRoundtrip(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-mcpv5-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-mcpv5-") })
	writeKnowledge(t, `{"topic":"e2e-mcpv5-k","summary":"실 프로세스 MCP 프로브","confidence":0.9}`)

	bin := rebuildBin(t)
	cmd := exec.Command(bin, "mcp")
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin 파이프: %v", err)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("mcp 프로세스 기동: %v", err)
	}
	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"flint_read","arguments":{"query":"e2e-mcpv5-k","limit":5}}}`,
	}, "\n") + "\n"
	if _, err := io.WriteString(stdin, reqs); err != nil {
		t.Fatalf("stdin 기록: %v", err)
	}
	stdin.Close() // EOF → 서버 정상 종료
	if err := cmd.Wait(); err != nil {
		t.Fatalf("mcp exit 오류(EOF 정상 0이어야 함): %v; out=%s", err, out.String())
	}

	var resps []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l != "" {
			resps = append(resps, decode(t, l))
		}
	}
	if len(resps) != 2 {
		t.Fatalf("응답 %d행, want 2; out=%s", len(resps), out.String())
	}
	for i, m := range resps {
		if m["id"] != float64(i+1) {
			t.Fatalf("응답[%d] id=%v, want %d(순서 대응); out=%s", i, m["id"], i+1, out.String())
		}
		if _, bad := m["error"]; bad {
			t.Fatalf("응답[%d] error=%v; out=%s", i, m["error"], out.String())
		}
	}
	initRes, _ := resps[0]["result"].(map[string]any)
	if initRes["protocolVersion"] == nil {
		t.Fatalf("initialize result에 protocolVersion 없음; out=%s", out.String())
	}
	callRes, _ := resps[1]["result"].(map[string]any)
	if callRes["isError"] != false {
		t.Fatalf("flint_read isError=%v, want false; out=%s", callRes["isError"], out.String())
	}
	content, _ := callRes["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("flint_read content 비어 있음; out=%s", out.String())
	}
	if text, _ := content[0].(map[string]any)["text"].(string); !strings.Contains(text, "e2e-mcpv5-k") {
		t.Fatalf("flint_read 결과에 씨딩 토픽 없음: %q", text)
	}
}

// ② 감사 세션 필터 — --session은 해당 [session:ID] 커밋만 반환한다.
func TestAuditSessionFilter(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-audit-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-audit-") })

	seed := func(session, topic string) {
		t.Helper()
		out, code := runCLI(t, []string{"write", "knowledge", "--session", session},
			`{"topic":"`+topic+`","summary":"감사 필터 프로브","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("write(%s) exit=%d, want 0; out=%s", session, code, out)
		}
	}
	seed("e2e-audit-s1", "e2e-audit-k1")
	seed("e2e-audit-s2", "e2e-audit-k2")

	out, code := runCLI(t, []string{"audit", "--session", "e2e-audit-s1"}, "")
	if code != 0 {
		t.Fatalf("audit exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	log, _ := m["log"].([]any)
	if len(log) == 0 {
		t.Fatalf("log 0행, want >=1; out=%s", out)
	}
	for _, e := range log {
		msg, _ := e.(map[string]any)["message"].(string)
		if !strings.Contains(msg, "[session:e2e-audit-s1]") {
			t.Fatalf("세션 필터 누출: %q", msg)
		}
		if strings.Contains(msg, "e2e-audit-s2") {
			t.Fatalf("다른 세션 커밋 혼입: %q", msg)
		}
	}
}

// ③ 백업 로테이션 — backup 3회(keep 2)면 가장 오래된 덤프+사이드카 쌍이
// 삭제되고 최신 2쌍만 남는다(T54 배선 실측: removed 필드로 삭제 수 보고).
func TestRotateE2E(t *testing.T) {
	pool := mustDB(t)
	setMeta(t, pool, "backup_keep", "2")
	t.Cleanup(func() { delMeta(t, pool, "backup_keep") })
	dir := t.TempDir()

	// mtime 강제 — 연속 실행은 같은 초에 몰려 정렬이 흔들리므로 결정적으로.
	base := time.Now().Add(-time.Hour)
	stamp := func(i int) {
		t.Helper()
		when := base.Add(time.Duration(i) * 10 * time.Minute)
		for _, suffix := range []string{"", ".sha256"} {
			_ = os.Chtimes(filepath.Join(dir, "d"+strconv.Itoa(i)+".sql"+suffix), when, when)
		}
	}
	backup := func(i int) string {
		t.Helper()
		out, code := runCLI(t, []string{"backup", "--out", filepath.Join(dir, "d"+strconv.Itoa(i)+".sql")}, "")
		if code != 0 {
			t.Fatalf("backup %d exit=%d, want 0; out=%s", i, code, out)
		}
		return out
	}
	// removed 추출 — backup --out 출력의 JSON 조립 부채(quoteJSON 뒤 stray
	// quote, 인시던트 기록)와 무관하게 로테이션 계약만 검증한다.
	removedOf := func(out string) int {
		t.Helper()
		m := regexp.MustCompile(`"removed":(\d+)`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("출력에 removed 없음: %s", out)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}

	backup(1)
	stamp(1)
	backup(2)
	stamp(2)
	out3 := backup(3) // 이때 rotate: d1 쌍 삭제
	if !strings.Contains(out3, `"status":"ok"`) {
		t.Fatalf("3회째 status 미보고; out=%s", out3)
	}
	if removed := removedOf(out3); removed != 2 { // d1.sql + d1.sql.sha256
		t.Fatalf("removed=%d, want 2(원본+사이드카); out=%s", removed, out3)
	}

	var sqls []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("백업 디렉터리 읽기: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			sqls = append(sqls, e.Name())
		}
	}
	if len(sqls) != 2 || sqls[0] != "d2.sql" || sqls[1] != "d3.sql" {
		t.Fatalf("잔존 덤프=%v, want [d2.sql d3.sql]", sqls)
	}
	for _, name := range sqls {
		if _, err := os.Stat(filepath.Join(dir, name+".sha256")); err != nil {
			t.Fatalf("사이드카 누락: %s.sha256", name)
		}
	}
}

// ④ 복구 훈련 + 제로히트 계측 — restore --drill은 flint_drill 스키마에
// 재생해 ok:true를 보고하고(원본 불변), read 1회 뒤 status에 read_stats가
// 노출된다(read_calls>=1).
func TestDrillE2EAndStats(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-drill-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-drill-") })
	delMeta(t, pool, "read_stats")
	t.Cleanup(func() { delMeta(t, pool, "read_stats") })

	writeKnowledge(t, `{"topic":"e2e-drill-k","summary":"드릴 프로브","confidence":0.9}`)
	dir := t.TempDir()
	dump := filepath.Join(dir, "drill.sql")
	if out, code := runCLI(t, []string{"backup", "--out", dump}, ""); code != 0 {
		t.Fatalf("backup exit=%d, want 0; out=%s", code, out)
	}

	out, code := runCLI(t, []string{"restore", "--drill", "--file", dump}, "")
	if code != 0 {
		t.Fatalf("restore --drill exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "drill" || m["ok"] != true {
		t.Fatalf("drill 출력=%v, want status=drill/ok=true", m)
	}
	if m["expected"] != m["restored"] || m["expected"] == float64(0) {
		t.Fatalf("expected=%v restored=%v, want 동일且 >=1", m["expected"], m["restored"])
	}
	// 드릴은 public 원본 불변 — 스키마 잔존 없음.
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'flint_drill'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("flint_drill 잔존=%d(항상 DROP이어야 함), err=%v", n, err)
	}

	// read 1회(히트) 후 status 노출.
	readLines(t, "e2e-drill-k")
	out, code = runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status exit=%d, want 0; out=%s", code, out)
	}
	sm := decode(t, out)
	if calls, _ := sm["read_calls"].(float64); calls < 1 {
		t.Fatalf("read_calls=%v, want >=1; out=%s", sm["read_calls"], out)
	}
	if _, ok := sm["read_zero_pct"]; !ok {
		t.Fatalf("status에 read_zero_pct 미노출; out=%s", out)
	}
}

// ⑤ 검색 랭킹 — topic 정합 행이 confidence 더 높은 요약 매칭 행보다 상단,
// 비정합 행끼리는 confidence 내림차순(T56: topic > confidence > 최신).
func TestReadRankingOrder(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-rank-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-rank-") })

	// 모든 행이 쿼리 "e2e-rank-k"에 매칭: A는 topic 정확 일치(신뢰 낮음),
	// B·C는 summary에 쿼리 포함(신뢰 B > C).
	writeKnowledge(t, `{"topic":"e2e-rank-k","summary":"정합 우선 프로브","confidence":0.5}`)
	writeKnowledge(t, `{"topic":"e2e-rank-a","summary":"e2e-rank-k 언급 높은 신뢰","confidence":0.9}`)
	writeKnowledge(t, `{"topic":"e2e-rank-b","summary":"e2e-rank-k 재인용 낮은 신뢰","confidence":0.8}`)

	out, code := runCLI(t, []string{"read", "e2e-rank-k", "--json", "--fields", "topic,confidence"}, "")
	if code != 0 {
		t.Fatalf("read exit=%d, want 0; out=%s", code, out)
	}
	var got []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			got = append(got, decode(t, l))
		}
	}
	if len(got) != 3 {
		t.Fatalf("결과 %d건, want 3; out=%s", len(got), out)
	}
	wantTopics := []string{"e2e-rank-k", "e2e-rank-a", "e2e-rank-b"}
	for i, want := range wantTopics {
		if got[i]["topic"] != want {
			t.Fatalf("순위[%d] topic=%v, want %v(topic 정합 > confidence); 전체=%s",
				i, got[i]["topic"], want, out)
		}
	}
}
