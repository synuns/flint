//go:build e2e

// e2e — 실 Doltgres 대상 T11 3케이스. 실행: go test -tags e2e ./tests/e2e/
// (make e2e). DB 미기동 시 전체 스킵. 테이블 정리는 DELETE FROM 기반
// (Doltgres는 TRUNCATE RESTART IDENTITY 미지원, T7 인시던트).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/cli"
	"flint/lib/db"
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
